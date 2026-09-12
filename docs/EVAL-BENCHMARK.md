# OpenModelPool 开放评测基准（Eval Benchmark）

> Phase 4 · 教育科研。目标：让任何教育/研究机构都能**公开、可复现**地评估
> 社区免费池里的模型，并把结果自愿回传为贡献数据。评测本身**不产生任何
> 激励、积与排名**——它只是"被看见"的另一面：把模型能力与免费参数的实情
> 摊开在阳光下，谁都可以自己跑一遍。

## 1. 数据源：公开模型目录

无需鉴权即可取当前网关的**公开面**（免费池 + 联邦节点主动共享的模型），
隐私红线：永不包含本机私有 provider：

```
GET /api/public/model-directory
```

返回 `ModelDirectorySnapshot`：

```json
{
  "snapshot_at": "2026-09-12T19:00:00Z",
  "total_models": 12,
  "free_pool_count": 10,
  "mesh_sources": ["free-pool", "edu-peer"],
  "models": [
    { "id": "openai/gpt-4o-mini", "sources": ["free-pool"], "free_pool": true },
    { "id": "edu/math-tutor",     "sources": ["edu-peer"], "free_pool": false }
  ]
}
```

## 2. 评测集（固定、跨模型不变）

五条中文基线题覆盖 知识/数学/逻辑/代码/翻译，按 `model_id, question_id,
question, answer_reference` 组织。答案不评判对错，只要求评测者**把模型原始
输出原样存档**，方便社区对照。

| qid | 类别 | 问题 |
|-----|------|------|
| q1-zh-general | 知识 | 用三句话解释"算力即服务"这个说法的利与弊。 |
| q2-zh-math | 数学 | 把 0.666666…（循环小数）化为最简分数，并说明步骤。 |
| q3-zh-logic | 逻辑 | A 比 B 快，B 比 C 快，C 和 D 一样快。D 和 A 谁快？为什么？ |
| q4-zh-code | 代码 | 用 Python 写一个函数，输入正整数 n，返回其所有真因子（不含自身）。 |
| q5-zh-translate | 翻译 | 把"Open source is not just code; it is a commons."译成中文。 |

鼓励扩展：机构可增加学科专用题，只要在 CSV 里如实标注 `vendor_qid` 即可。

## 3. 复现流程

```powershell
# 1. 拉当前公开模型目录
Invoke-RestMethod http://localhost:8000/api/public/model-directory | Out-File model-directory.json

# 2. 对每个 free_pool=true 的模型逐题调用（OpenAI 兼容）
#    POST /v1/chat/completions  Authorization: Bearer <本网关接受的 key>
#    body: {"model":"<模型ID>","messages":[{"role":"user","content":"<问题>"}]}

# 3. 记录每题的 {model_id, qid, output, prompt_tokens, completion_tokens, latency_ms, model}
#    四舍五入到整毫秒，避免假精度。
```

> **节流守则**：公共免费池是社区共享资源。批量评测**不得**压满上游限频
> （建议每次 <=5 并发、题间留 1s）。同一模型同一机构每天最多跑一轮完整
> 评测。

## 4. 结果回传（可选、自愿）

- 评测 CSV 与贡献数据同构，可先导出本地账本对齐列名
  （`GET /api/admin/ledger/export?format=csv`）。
- **自愿上传**：提 PR 到仓库 `docs/eval-results/<机构名>-<日期>.csv`
  （去掉含 key 的字段）。不做排名榜单、不按结果授予任何额度——结果只供
  社区对照使用，防止"刷榜换激励"把公益带偏。

## 5. 一致性与红线

- 端点数据面与 `/v1/models` 联邦聚合同一来源，但比它**更窄**：只含社区公共
  面；`/v1/models` 的本机私有模型永不出现。
- 评测不涉及任何溯源身份或鉴权扩展；没有新加密、没有新信任模型。
- 本基准**不承诺**模型能力等价性——免费池上游可能限频/限并发，延迟与
  token 统计仅供教学与研究对照，不作商业 SLA 依据。