# OpenModelPool 推广物料包

> 状态：**待雷工审核**。本文件只是物料草稿，代理不会自行发布任何内容。
> 生成日期：2026-08-09 · 对应版本：v4.3.24

---

## 一、一句话定位

**中文**：OpenModelPool 是一个纯公益的开放模型池——单个 Go 二进制，把你手里各家大模型 API 聚合成统一的 OpenAI 兼容网关，并可选地把用不完的闲置额度共享给社区。

**English**：OpenModelPool is a non-profit open model pool — a single Go binary that turns your scattered LLM API keys into one OpenAI-compatible gateway, and optionally shares the quota you'd otherwise waste.

GitHub 仓库 About 栏（≤ 350 字符，建议直接用英文版）：

```
Non-profit OpenAI-compatible gateway that pools multiple LLM APIs into one endpoint. Single self-hosted Go binary, no web framework, minimal dependencies. Optional federation lets nodes share idle quota. No token, no points economy, no skim.
```

---

## 二、README 润色（已落地）

本轮已对 `README.md` 做的改动，发布前请复核：

| 位置 | 改动 |
|------|------|
| 标题下副标题 | 从"Personal Model Proxy + Geek Sharing Network"改为直述公益定位 |
| 副标题下新增一段 | 明确"无商业模式 / 无代币 / 无积分经济 / 无抽成"，并链接中英文公益宣言 |
| 版本徽章 | v4.1.6 → v4.3.24（此前与 `AppVersion` 漂移） |
| 新增徽章 | non-profit · no token · no skim |
| "What Is It?" 引用块 | 把"赚取 Contribution Credits / 兑换资源"的经济学措辞，改写为"账本记账、1:1 归还、可不领取"的公益表述，并补一段"这是记账不是货币"的澄清 |
| "Our Belief" 章节末 | 新增 **The pledge, in four lines**（不收钱 / 默认善意 / 不吹没做的 / 贡献者共治） |
| Contribution Credit System | Earn/Spend 两条改写：明确 1:1 无手续费；额度用尽**不是拒绝**，回落到对所有人开放的社区免费池 |

发布前建议再人工确认两点：

1. README 中仍有若干 ⚠️「Planned / Not Yet Wired」条目（BIP39 助记词身份、DHT 关闭、区域路由 stub），这些是**刻意保留的诚实标注**，不要为了好看删掉——这正是项目的差异化卖点。
   - **「5 维路由只有 4 个滑块」不是缺口、不要补第 5 个滑块**：后台路由是 5 维加权打分模型（Trust / Reputation / Latency / Availability / Contribution，见 `network_loadbalancer.go` 的 `ScoreNode` 与 `LBConfig` §9.2），但第 5 个"维度"是**路由算法本身**（`ScoreNode` 加权合成 + 区域加减分 + `SelectNode` 选择逻辑），属固定后台实现、用户不可调。用户能设置的 4 个滑块（优先级 / 成本 / 延迟 / 额度）已覆盖全部可调权重，因此 4 滑块是设计使然而非 unfinished work。
   - ✅ **已同步进 README**：该条已从「Planned / Not Yet Wired」表格移出（列在那里等于把设计说成缺陷），改为表格下方的 "Not a gap — by design" 澄清块；`4-Dimension Intelligent Routing` 与 `Health-Aware Load Balancer` 两节也各补了一句指回该澄清。
2. README 通篇使用 emoji 作为章节图标（🤖🌍📋 等），与近期文档风格不一致。是否统一去除，请雷工拍板；本轮未擅自改动。

---

## 三、中文发布稿草稿（约 300 字）

> 用途：V2EX / 少数派 / 掘金 / 微信技术群首发。可直接复制。

**OpenModelPool：一个纯公益的开放模型池**

手里攒了七八个大模型 API key，每个月都有用不完的额度白白过期——这大概是不少人的现状。

OpenModelPool 想解决的就是这件事。它是一个单文件 Go 程序，编译产物就一个二进制，下载即跑，不用装数据库、不用配 Web 服务器。启动后把你所有的模型 API 聚合成一个统一的 OpenAI 兼容端点，自动故障转移、按优先级/成本/延迟/额度四维路由，配 base URL 和 key 就能接入任何现有客户端。除 OpenAI 格式外，也原生支持 Anthropic、Gemini、Azure 的调用格式。

如果你愿意，还可以把闲置额度共享给社区，组成一个联邦网络。账本 1:1 记录你贡献了多少、可以取回多少，全程透明可导出——但它不是积分，不是代币，不能交易，项目方不抽任何一分成。没贡献的人也照用不误：默认所有人都是好人，系统只防恶意滥用，不防"白嫖"。

项目纯公益，MIT 协议，没有商业模式，也不打算有。README 里把还没做完的部分都用 ⚠️ 明确标了出来。

也欢迎大家一起来迭代：想加什么功能、觉得哪里设计得不对，都可以开 Issue 聊；路线图（docs/BACKLOG.md）是公开的，认领一项直接提 PR 就行。这个项目属于所有用它的人。

GitHub：github.com/lisiyu/openmodelpool

（字数：约 390 字，可按平台裁剪至 300 字以内——删掉第二段"除 OpenAI 格式外…"一句、并把共建段压成"欢迎开 Issue 提想法、认领 BACKLOG 提 PR"即可。）

---

## 四、English release blurb（Hacker News / Reddit r/selfhosted）

**Title**: `Show HN: OpenModelPool – a non-profit OpenAI-compatible gateway that pools your idle LLM quota`

**Body** (~120 words):

> I had API keys for six different model providers and let unused quota expire every month. OpenModelPool is what I built about it.
>
> It's a single Go binary — no web framework, no ORM, no database to install; five direct dependencies total. It fronts all your provider keys behind one OpenAI-compatible endpoint with failover and 4-dimension routing (priority / cost / latency / quota). Anthropic, Gemini and Azure request formats work natively too — same base URL, same key.
>
> Optionally, nodes federate and share idle quota. The ledger records contributions 1:1 and is fully exportable, but it is deliberately **not** a token: not tradeable, not withdrawable, and the project takes no cut. Non-contributors are never blocked.
>
> Non-profit, MIT, no business model. Everything not yet implemented is marked ⚠️ in the README.
>
> Contributions and design input are genuinely welcome — the roadmap lives in `docs/BACKLOG.md`, open an issue to propose a feature or claim an item and send a PR.

---

## 五、GitHub Topics 建议

按相关度排序，GitHub 最多 20 个，建议取前 12–15 个：

```
llm-gateway
openai-api
openai-compatible
ai-gateway
llm-proxy
api-gateway
self-hosted
golang
single-binary
anthropic-api
gemini-api
azure-openai
federation
p2p
non-profit
```

说明：
- `llm-gateway` / `openai-compatible` / `llm-proxy` 是这个品类被搜索最多的词，必留。
- `self-hosted` 能吃到 r/selfhosted 与 awesome-selfhosted 的自然流量。
- `single-binary` + `golang` 是本项目的真实差异点（单二进制自托管、无框架无数据库），值得占位。
- 不建议加 `decentralized-ai`、`web3`、`dao` 这类词——会把项目推到代币叙事的语境里，与公益定位冲突。

---

## 六、发布前检查清单

- [ ] `go build ./...` / `go test ./...` 全绿（本轮已验证：build EXIT=0，test EXIT=0，耗时约 72s）
- [x] README 版本徽章与 `main.go` 的 `AppVersion` 一致（2026-08-10 复核：两处均为 **4.3.32**，`scripts/omp-manager.ps1` fallback 同步为 v4.3.32，与 tag `v4.3.32` 一致，无漂移）
- [ ] 决定 README 章节 emoji 是否保留（见第二节第 2 点）
- [x] **补共建入口**：已在 README 新增 `## 🤝 Contributing` 章节（四类入口表 + PR 前门禁 + "不会合并代币/积分/付费层"红线），指向公开路线图 `docs/BACKLOG.md`
- [x] **补齐社区协作基建（2026-08-09，v4.3.29 后）**：`CONTRIBUTING.md`、`SECURITY.md`、`.github/ISSUE_TEMPLATE/{bug_report,feature_request,config}.yml`、`.github/PULL_REQUEST_TEMPLATE.md` 均已落地；README 贡献表改为模板直链并补安全上报行，`docs/INDEX.md` 贡献者区同步。**发布前请在 GitHub 仓库设置确认 Issues 已开启、模板生效**
- [x] **测试门禁已可信**：修掉了约 1/3 概率随机失败的 `TestHB10_MultiUser_RecordConsumerUsage`（详见 BACKLOG P5-1，并由此挖出关机丢失消费者用量的真实 bug）。第一批 clone 项目的人按 README 跑 `go test ./...` 不会再随机翻车——在 Show HN 场景下这条尤其致命
- [x] **CI 与本地口径已对齐（2026-08-10，BACKLOG P5-3）**：CI 硬门禁原先跑 `-race -short`，而仓库里根本没有 `testing.Short()` 分支、没有任何 build tag——那个 `-short` 一个用例都没排除，"unit / integration 分层"是纸面上的。现改为 `go test -race -count=1 ./...`（`-count=1` 关掉测试缓存，避免缓存命中的 PASS 冒充本次通过），与贡献者本地口径一致；第二个 job 改为独立复跑抓 flaky 的软门禁。**对外可以说"CI 跑的就是你本地那套"而不心虚**
- [x] **已修正不实陈述：「零第三方依赖」**。`go.mod` 实际有 5 个直接依赖（`golang-jwt/jwt`、`golang.org/x/crypto`、`golang.org/x/net`、`go-bip39`、`chromedp`）+ 6 个间接依赖。原中英文发布稿与 About 栏均写了"零第三方依赖 / standard library only, no third-party deps"——**任何人 `cat go.mod` 即可证伪**，发在 HN 上会直接摧毁"诚实标注"这个核心卖点。已全部改为"单二进制自托管 / 无 Web 框架无 ORM 无数据库 / 五个直接依赖"的准确表述
- [ ] `docs/PUBLIC-WELFARE.md` 与 `.en.md` 两版内容仍然对齐
- [ ] 仓库 About 栏与 Topics 按第一、五节填写
- [ ] 手动 push + 打 tag 触发 release（**由雷工执行**）
- [ ] 首发渠道顺序建议：GitHub Release → V2EX/掘金（中文稿）→ Hacker News Show HN（英文稿，选北京时间 22:00–24:00 发）

---

## 七、不做什么

为避免物料在传播中走偏，以下表述**一律不用**：

- 「积分」「代币」「Token 经济」「挖矿」「激励层」「抽成」「分润」
- 「去中心化 AI 算力市场」「AI 版 BitTorrent 经济模型」
- 任何暗示未来会发币、会商业化、会有付费版的说法
- 任何把尚未实现的功能（BIP39 身份、DHT、区域路由）说成已完成
