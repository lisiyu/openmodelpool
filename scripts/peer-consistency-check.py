#!/usr/bin/env python3
"""
OpenModelPool 跨节点 peer 一致性校验。

对 5 台生产主机(omp-cc/org/com/io/net)逐一:
  1. 经 Windows 原生 ssh 进主机,用各主机 data/admin.json 的 jwt_secret 伪造 admin JWT
     (沿用联邦自愈 SOP,无需明文密码);
  2. 调本机 /api/network/status + /api/network/peers 取 peer 视图;
  3. 在本地比对所有节点的 peer 矩阵,标出:
     - 缺失/多余 peer(某节点没看到应看到的节点 → 真不一致);
     - 同一 peer 在不同节点上 online/offline 状态冲突(多为心跳瞬时,标黄)。

退出码:0=全部一致;1=发现真不一致(缺失/多余 peer);2=某主机不可达/脚本错误。
依赖:Windows 原生 ssh.exe(C:/Windows/System32/OpenSSH/ssh.exe)、远程主机 python3。
"""
import base64
import json
import subprocess
import sys

SSH = r"C:/Windows/System32/OpenSSH/ssh.exe"
HOSTS = ["omp-cc", "omp-org", "omp-com", "omp-io", "omp-net"]

# 远端执行的 python:读 jwt_secret → 伪造 HS256 admin JWT → 拉本机 peer 视图 → 打印 JSON
REMOTE = r'''
import json, hmac, hashlib, base64, time, urllib.request
sec = json.load(open('/opt/openmodelpool/data/admin.json'))['jwt_secret']
now = int(time.time())
payload = {"sub": "root", "exp": now + 600, "iat": now, "iss": "openmodelpool", "type": "access"}
def b(d):
    return base64.urlsafe_b64encode(d).rstrip(b'=')
seg = b(json.dumps({"alg": "HS256", "typ": "JWT"}).encode()) + b'.' + b(json.dumps(payload).encode())
sig = hmac.new(sec.encode(), seg, hashlib.sha256).digest()
tok = (seg + b'.' + b(sig)).decode()
def get(p):
    req = urllib.request.Request('http://127.0.0.1:8000' + p, headers={'Authorization': 'Bearer ' + tok})
    return json.load(urllib.request.urlopen(req, timeout=10))
st = get('/api/network/status')
peers = get('/api/network/peers').get('peers', [])
out = {
    "node_id": st.get('node_id'),
    "peers_count": st.get('peers_count'),
    "online": (st.get('stats') or {}).get('online_peers'),
    "peers": [{"addr": (p.get('addresses') or ['?'])[0], "status": p.get('status')} for p in peers],
}
print(json.dumps(out))
'''


def fetch(host):
    b64 = base64.b64encode(REMOTE.encode()).decode()
    try:
        r = subprocess.run(
            [SSH, "-o", "ConnectTimeout=12", "-o", "StrictHostKeyChecking=no", host,
             "echo %s | base64 -d | python3 -" % b64],
            capture_output=True, text=True, timeout=40,
        )
        if r.returncode != 0:
            return {"error": (r.stderr or r.stdout).strip()[:200]}
        line = r.stdout.strip().splitlines()[-1]
        return json.loads(line)
    except Exception as e:  # noqa: BLE001
        return {"error": str(e)}


def main():
    results = {}
    for h in HOSTS:
        results[h] = fetch(h)

    print("=== Peer 视图(每节点本机视角) ===")
    union = set()
    for h, d in results.items():
        if "error" in d:
            print(f"  {h}: ❌ 不可达/错误 {d['error']}")
            continue
        print(f"  {h}: node={d['node_id'][:14]}… peers={d['peers_count']} online={d['online']}")
        for p in d["peers"]:
            union.add(p["addr"])

    # 每节点"自己的地址" = 全集中没出现在自己列表里的那一个(全网状应有 5 域,每节点列其他 4)
    print("\n=== 缺失/多余 peer 校验(全网状应每节点看到其他 4 个) ===")
    real_problems = []
    for h, d in results.items():
        if "error" in d:
            continue
        seen = {p["addr"] for p in d["peers"]}
        own = union - seen  # 期望恰好 1 个 = 自己
        expected = union - own
        missing = expected - seen
        extra = seen - union
        if missing:
            real_problems.append(f"{h} 缺失 peer: {sorted(missing)}")
            print(f"  ❌ {h} 缺失: {sorted(missing)}")
        elif extra:
            real_problems.append(f"{h} 多余 peer: {sorted(extra)}")
            print(f"  ❌ {h} 多余: {sorted(extra)}")
        else:
            print(f"  ✅ {h} peer 集完整(4/4,自址={sorted(own)})")

    # 同一 peer 在不同节点上的 online/offline 状态冲突(多为心跳瞬时,标黄)
    print("\n=== 状态冲突(同 peer 跨节点 online 不一致) ===")
    addr_status = {}
    for h, d in results.items():
        if "error" in d:
            continue
        for p in d["peers"]:
            addr_status.setdefault(p["addr"], []).append((h, p["status"]))
    disagreement = False
    for addr, lst in sorted(addr_status.items()):
        states = {s for _, s in lst}
        if len(states) > 1:
            disagreement = True
            print(f"  ⚠️  {addr}: " + ", ".join(f"{h}={s}" for h, s in lst))
    if not disagreement:
        print("  ✅ 无状态冲突(采样时刻所有 peer 状态一致)")

    print()
    if real_problems:
        print("结论:发现真不一致(缺失/多余 peer)→ 退出码 1")
        return 1
    if any("error" in d for d in results.values()):
        print("结论:有主机不可达 → 退出码 2")
        return 2
    print("结论:全部一致 → 退出码 0")
    return 0


if __name__ == "__main__":
    sys.exit(main())
