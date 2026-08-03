# NodeIdentity

NodeIdentity 是节点的去中心化身份，基于 Ed25519 密钥对和 BIP39 助记词，用于联邦认证和中继签名。

## 什么是 NodeIdentity？

每个 OpenModelPool 节点拥有唯一的 Ed25519 密钥对，私钥从 BIP39 助记词按 SLIP-0010 路径 `m/44'/2024'/0'` 派生。节点 ID 是公钥的 base64 编码前缀，格式为 `mmx-<base64>`。

**关键特征**:
- Ed25519 签名算法（64 字节签名）
- BIP39 助记词备份（12/24 词）
- SLIP-0010 密钥派生（路径 m/44'/2024'/0'）
- 私钥加密存储（AES-256-GCM）

## 代码位置

| 方面 | 位置 |
|------|------|
| 类型定义 | `node.go` — `NodeIdentity` struct |
| 密钥派生 | `node.go` — `privateKey()` |
| 签名/验证 | `node.go` — `Sign()`/`NodeID()`/`PubKeyB64()` |
| 创世块 | `genesis.go` — `GenesisBlock` |

## 结构

```go
type NodeIdentity struct {
    mu             sync.RWMutex
    nodeID         string
    privKey        ed25519.PrivateKey
    encPrivKey     string          // 加密后的私钥
    pubKey         ed25519.PublicKey
    mnemonic       string
    hasMnemonic    bool
    backupConfirmed bool
    keyPath        string          // data/.node_key
}
```

### NodeID 格式

```
mmx-<base64(Ed25519_PublicKey)[:20]>
```

- 前缀 `mmx-` 标识 OpenModelPool 节点
- 后缀为 Ed25519 公钥的 base64 编码子串

## 不变量

1. **NodeID 唯一性**: 每个密钥对对应唯一 NodeID
2. **私钥不可导出**: 私钥仅用于内存签名，加密存储在磁盘
3. **助记词一次性展示**: BIP39 助记词仅在创建时展示一次

## 关系

| 关联概念 | 关系 | 描述 |
|---------|------|------|
| FederationManager | 身份认证 | 联邦请求使用 NodeIdentity 签名 |
| TransportEncryption | 密钥交换 | 传输加密使用 Ed25519 密钥派生共享密钥 |
| GenesisBlock | 网络锚定 | 创世块绑定网络 ID |
