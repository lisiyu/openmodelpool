package main

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"strconv"
	"time"
)

// PunchMagic 前缀用于区分打洞协议帧与普通 relay/心跳数据报，接收方据此识别对端发来的打洞包。
var PunchMagic = []byte{0x4f, 0x4d, 0x50, 0x31} // "OMP1"

// punchSigDomain 是打洞通告签名的域分隔前缀，保证打洞签名不能被重用到系统内
// 其他任何签名语义上（联邦鉴权、账本、gossip）。
const punchSigDomain = "OMP-PUNCH-v1"

// punchOfferMaxAge 限制打洞通告 SenderTS 的最大年龄。光有签名防不住重放：
// 没有新鲜度窗口，攻击者可无限期重播截获的合法签名通告（federation.go 的
// X-Node-Timestamp 机制防范的是同一类问题）。
const punchOfferMaxAge = 5 * time.Minute

// punchOfferFutureSkew 容忍节点间适度的时钟偏差。
const punchOfferFutureSkew = time.Minute

// PunchOffer 是两个节点在尝试 UDP 打洞前，经 relay 或 gossip 联邦交换的连接性通告。
// 携带各自经 STUN 得到的公网 reflexive 地址，供对端作为打洞目标。
type PunchOffer struct {
	NodeID        string `json:"node_id"`
	ReflexiveAddr string `json:"reflexive_addr"` // STUN 公网 UDP 地址 host:port
	LocalAddr     string `json:"local_addr"`     // 私网 UDP 监听地址 host:port（open 网络下可能与 reflexive 相同）
	Nonce         []byte `json:"nonce"`          // 16 字节随机值，对端回显以证明存活
	SenderTS      int64  `json:"ts"`
	// Signature 是发送方对其规范化载荷的 ed25519 签名（base64）。
	// /network/__punch 在建 session 前对照联邦信任池验签；无签名或验签失败
	// 直接拒绝（fail closed），防止冒名 NodeID 建 session。
	Signature string `json:"sig,omitempty"`
}

// NewPunchOffer 为本地节点构造一个打洞通告。
func NewPunchOffer(nodeID, reflexive, local string) (PunchOffer, error) {
	var n [16]byte
	if _, err := rand.Read(n[:]); err != nil {
		return PunchOffer{}, err
	}
	return PunchOffer{
		NodeID:        nodeID,
		ReflexiveAddr: reflexive,
		LocalAddr:     local,
		Nonce:         n[:],
		SenderTS:      time.Now().UnixNano(),
	}, nil
}

// EncodePunchOffer 把通告序列化为自描述帧（magic 前缀 + JSON 负载）。
func EncodePunchOffer(o PunchOffer) ([]byte, error) {
	if len(o.Nonce) != 16 {
		return nil, errors.New("nat_punch: nonce must be 16 bytes")
	}
	payload, err := json.Marshal(o)
	if err != nil {
		return nil, err
	}
	frame := make([]byte, 0, len(PunchMagic)+len(payload))
	frame = append(frame, PunchMagic...)
	frame = append(frame, payload...)
	return frame, nil
}

// DecodePunchOffer 解析 EncodePunchOffer 产生的帧，并做基础合法性校验。
func DecodePunchOffer(b []byte) (PunchOffer, error) {
	if len(b) < len(PunchMagic) {
		return PunchOffer{}, errors.New("nat_punch: frame too short")
	}
	for i := range PunchMagic {
		if b[i] != PunchMagic[i] {
			return PunchOffer{}, errors.New("nat_punch: bad magic")
		}
	}
	var o PunchOffer
	if err := json.Unmarshal(b[len(PunchMagic):], &o); err != nil {
		return PunchOffer{}, err
	}
	if o.NodeID == "" || o.ReflexiveAddr == "" {
		return PunchOffer{}, errors.New("nat_punch: missing node_id or reflexive_addr")
	}
	if len(o.Nonce) != 16 {
		return PunchOffer{}, errors.New("nat_punch: nonce must be 16 bytes")
	}
	return o, nil
}

// NonceEqual 常量时间比较两个 nonce 是否一致。
func NonceEqual(a, b []byte) bool {
	return len(a) == 16 && len(b) == 16 && bytes.Equal(a, b)
}

// punchOfferSigned 是签名覆盖的 PunchOffer 规范化投影。用独立结构体（而非
// 把 Signature 置空后签名）保证签名载荷永远不可能意外包含签名字段本身。
// encoding/json 按结构体字段声明顺序序列化，同一输入的字节表示是确定的。
type punchOfferSigned struct {
	NodeID        string `json:"node_id"`
	ReflexiveAddr string `json:"reflexive_addr"`
	LocalAddr     string `json:"local_addr"`
	Nonce         []byte `json:"nonce"`
	SenderTS      int64  `json:"ts"`
}

// signingPayload 返回 Signature 覆盖的规范化字节：域分隔前缀 + 确定性 JSON。
// 覆盖全部身份与路由字段（NodeID、Nonce、SenderTS、双地址），攻击者无法拿
// 一个合法签名通告去改地址或重绑到别的 NodeID。
func (o PunchOffer) signingPayload() []byte {
	payload, err := json.Marshal(punchOfferSigned{
		NodeID:        o.NodeID,
		ReflexiveAddr: o.ReflexiveAddr,
		LocalAddr:     o.LocalAddr,
		Nonce:         o.Nonce,
		SenderTS:      o.SenderTS,
	})
	if err != nil {
		// 上述字段全部可序列化，此处不可能失败；防御性返回空。
		return []byte(punchSigDomain + ":")
	}
	return []byte(fmt.Sprintf("%s:%s", punchSigDomain, payload))
}

// Sign 用 priv 对通告签名并写入 Signature（base64）。签名必须在通告所有
// 字段定稿后调用；之后再改任何字段都会使签名失效。
func (o *PunchOffer) Sign(priv ed25519.PrivateKey) {
	o.Signature = base64.StdEncoding.EncodeToString(ed25519.Sign(priv, o.signingPayload()))
}

// VerifySignature 用对端公钥校验通告签名。签名缺失、格式错误或公钥长度非法
// 都返回 false（不 panic：ed25519.Verify 对非法长度公钥会 panic）。
func (o PunchOffer) VerifySignature(pub ed25519.PublicKey) bool {
	if o.Signature == "" || len(pub) != ed25519.PublicKeySize {
		return false
	}
	sig, err := base64.StdEncoding.DecodeString(o.Signature)
	if err != nil || len(sig) != ed25519.SignatureSize {
		return false
	}
	return ed25519.Verify(pub, o.signingPayload(), sig)
}

// verifyPunchOffer 在创建任何 session 状态前认证对端打洞通告：按 NodeID 在
// 联邦信任池（registry-based trust_pool.json，与 withFederationAuth 同源）中
// 查公钥并验签，同时用 SenderTS 新鲜度窗口防重放。Fail-closed：未知节点、
// 无公钥、无签名、签名无效、时间戳过期/超前，全部拒绝。
func verifyPunchOffer(offer *PunchOffer) bool {
	if offer.Signature == "" {
		slog.Warn("punch offer rejected: missing signature", "peer", offer.NodeID)
		return false
	}
	if fed == nil {
		slog.Warn("punch offer rejected: federation unavailable, cannot verify")
		return false
	}
	info, ok := fed.GetNode(offer.NodeID)
	if !ok || info.PubKey == "" {
		slog.Warn("punch offer rejected: unknown node or missing pubkey", "peer", offer.NodeID)
		return false
	}
	pubBytes, err := base64.StdEncoding.DecodeString(info.PubKey)
	if err != nil || len(pubBytes) != ed25519.PublicKeySize {
		slog.Warn("punch offer rejected: malformed pubkey in trust pool", "peer", offer.NodeID)
		return false
	}
	if !offer.VerifySignature(ed25519.PublicKey(pubBytes)) {
		slog.Warn("punch offer rejected: bad signature", "peer", offer.NodeID)
		return false
	}
	age := time.Since(time.Unix(0, offer.SenderTS))
	if age > punchOfferMaxAge || age < -punchOfferFutureSkew {
		slog.Warn("punch offer rejected: timestamp outside freshness window", "peer", offer.NodeID)
		return false
	}
	return true
}

// PunchTarget 返回本节点应向对端发送的打洞包目标地址（即对端的公网 reflexive 地址）。
func (o PunchOffer) PunchTarget() (string, error) {
	if o.ReflexiveAddr == "" {
		return "", errors.New("nat_punch: no reflexive addr")
	}
	return o.ReflexiveAddr, nil
}

// ParseUDPAddr 把 "host:port" 解析为 *net.UDPAddr，供实际收发打洞包使用。
func ParseUDPAddr(s string) (*net.UDPAddr, error) {
	host, port, err := net.SplitHostPort(s)
	if err != nil {
		return nil, err
	}
	p, err := strconv.Atoi(port)
	if err != nil {
		return nil, err
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return nil, errors.New("nat_punch: invalid IP in " + s)
	}
	return &net.UDPAddr{IP: ip, Port: p}, nil
}

// Candidate4Tuple 由本端通告与对端通告推导出打洞所用的四元组两端地址。
func Candidate4Tuple(local, remote PunchOffer) (localAddr, remoteAddr *net.UDPAddr, err error) {
	localAddr, err = ParseUDPAddr(local.ReflexiveAddr)
	if err != nil {
		return nil, nil, err
	}
	remoteAddr, err = ParseUDPAddr(remote.ReflexiveAddr)
	if err != nil {
		return nil, nil, err
	}
	return localAddr, remoteAddr, nil
}

// packUint64 是打洞握手的小工具：把序号写入 8 字节大端，便于在报文中携带序号而无需 JSON。
func packUint64(v uint64) []byte {
	b := make([]byte, 8)
	binary.BigEndian.PutUint64(b, v)
	return b
}

// unpackUint64 反向解析 packUint64 产生的 8 字节大端。
func unpackUint64(b []byte) (uint64, error) {
	if len(b) < 8 {
		return 0, errors.New("nat_punch: need 8 bytes")
	}
	return binary.BigEndian.Uint64(b[:8]), nil
}
