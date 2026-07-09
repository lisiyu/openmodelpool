# OpenModelPool 部署脚本

## 快速部署

### 服务端（中转服务器）

在你的 Ubuntu 服务器上执行：

```bash
curl -sL https://raw.githubusercontent.com/lisiyu/openmodelpool/main/scripts/frps-deploy.sh | sudo bash
```

或手动下载：
```bash
wget https://raw.githubusercontent.com/lisiyu/openmodelpool/main/scripts/frps-deploy.sh
sudo bash frps-deploy.sh
```

**注意**：请确保云服务商安全组放行 `7000/tcp` 和 `8000/tcp` 端口。

### 客户端（OpenModelPool 节点）

在运行 OpenModelPool 的机器上执行：

```bash
curl -sL https://raw.githubusercontent.com/lisiyu/openmodelpool/main/scripts/frpc-connect.sh | sudo bash -s -- <服务器IP>
```

例如：
```bash
curl -sL https://raw.githubusercontent.com/lisiyu/openmodelpool/main/scripts/frpc-connect.sh | sudo bash -s -- 1.2.3.4
```

## 自定义 Token

如需自定义认证 Token，设置环境变量 `FRP_TOKEN`：

```bash
export FRP_TOKEN="your-custom-token"
# 服务端和客户端需使用相同 Token
```

## 架构说明

```
用户请求 → 服务器:8000 (frps) → 隧道 → 云主机:8000 (OpenModelPool)
```

- **frps** (服务端): 运行在中转服务器上，暴露公网端口
- **frpc** (客户端): 运行在 OpenModelPool 节点上，建立隧道连接
- 所有通信通过加密隧道，Token 认证
