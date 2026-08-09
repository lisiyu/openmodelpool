package main

import (
	"bytes"
	"crypto/rand"
	"encoding/binary"
	"encoding/json"
	"errors"
	"net"
	"strconv"
	"time"
)

// PunchMagic 前缀用于区分打洞协议帧与普通 relay/心跳数据报，接收方据此识别对端发来的打洞包。
var PunchMagic = []byte{0x4f, 0x4d, 0x50, 0x31} // "OMP1"

// PunchOffer 是两个节点在尝试 UDP 打洞前，经 relay 或 gossip 联邦交换的连接性通告。
// 携带各自经 STUN 得到的公网 reflexive 地址，供对端作为打洞目标。
type PunchOffer struct {
	NodeID        string `json:"node_id"`
	ReflexiveAddr string `json:"reflexive_addr"` // STUN 公网 UDP 地址 host:port
	LocalAddr     string `json:"local_addr"`      // 私网 UDP 监听地址 host:port（open 网络下可能与 reflexive 相同）
	Nonce         []byte `json:"nonce"`           // 16 字节随机值，对端回显以证明存活
	SenderTS      int64  `json:"ts"`
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
