# Youtu-RAG Go Service 化推进计划

这份文档回答一个问题：

> 已经有了 `youtu-rag-service` 这一阶段成果之后，如果不追求把所有 Python 都翻译成 Go，而是允许保留一部分 Python，我们后面怎么把整个 Youtu-RAG 做成一个长期可运行、可维护、可扩展的服务？

结论先说清楚：

- `fuzzy-search` 是核心，但不是完整产品。
- 完整 Youtu-RAG 还包括数据上传、文档解析、图构建、schema 管理、问题分解、检索、答案生成、agent 多轮推理、可视化、评估、任务进度、缓存和运行时管理。
- 下一阶段不建议继续为了“全 Go”而迁移模型细节。
- 更合理的工程目标是：Go 做长期服务的稳定外壳和流程编排；Python 保留为模型/图构建/向量检索/LLM 推理 worker。

可以把它想成一家餐馆：

- Go 是前台、排队系统、点单系统、账本、出餐进度牌。
- Python 是厨房里的专业设备，比如切菜机、烤箱、咖啡机。
- 我们不需要把烤箱改造成 Go 写的烤箱；我们要先把餐馆服务流程做稳，让每个订单都能追踪、重试、交付和验收。

## 当前已有基础

当前 public repo：

<https://github.com/likun666661/youtu-rag-service>

当前 Go 侧已经做到：

- 可以读取 `youtu-graphrag` 生成的 graph 和 chunks。
- 可以通过 Python sidecar 调 embedding / FAISS / rerank / path2 primitive。
- 可以执行多种迁移模式：
  - `runtime-trace`
  - `primitive-merge`
  - `rerank-merge`
  - `native-path1-rerank`
- 当前主路径 `native-path1-rerank` 已经不再调用 Python `/v1/retrieval/path1-triples`。
- Go 已经能自己生成 path1 raw candidates，再交给 Python rerank primitive。
- demo gates 已经覆盖：
  - `loader`
  - `chunk`
  - `triple`
  - `full`
- 在 demo golden 上，当前主路径四档 gate 都是 0 diff。

这说明：检索核心已经有了一个可验证的 Go 工程底座。

但它还不是完整 Youtu-RAG 服务。它更像“检索发动机”，还不是“整辆车”。

## Youtu-RAG 的完整能力地图

重新看 `youtu-graphrag` 后，完整系统大概分成以下几块。

### 1. 数据接入

对应 Python 文件：

- `backend.py`
- `utils/document_parser.py`

能力：

- 上传 txt / md / json / pdf / docx / doc。
- 解析文档内容。
- 把上传文件整理成 dataset corpus。
- 保存到 `data/uploaded/<dataset>/corpus.json`。

这部分不是 fuzzy search，但它是用户开始使用系统的入口。

### 2. 图构建

对应 Python 文件：

- `models/constructor/kt_gen.py`
- `utils/tree_comm.py`

能力：

- 把 corpus 切成 chunks。
- 调 LLM 从文本里抽取：
  - attributes
  - triples
  - entity types
  - schema evolution 信息
- 写出 graph。
- 写出 chunk 文件。
- 做 triple deduplication。
- 做 community / super node 构建。

这部分是长期服务里最适合做成“异步 job”的能力，因为它耗时长、依赖 LLM、会产生大量 artifact。

### 3. Schema 和配置管理

对应 Python 文件：

- `config/base_config.yaml`
- `config/config_loader.py`
- `schemas/*.json`

能力：

- 管 dataset 路径。
- 管 schema。
- 管 prompt。
- 管 chunk size。
- 管 embedding model。
- 管 retrieval top-k。
- 管 agent max steps。

长期服务里，schema 和 config 不能只是散落在文件系统里。Go service 应该至少知道每个 dataset 的 schema、graph、chunks、cache 是否存在，以及状态是否可用。

### 4. 检索核心

对应 Python 文件：

- `models/retriever/enhanced_kt_retriever.py`
- `models/retriever/faiss_filter.py`
- `scripts/vector_sidecar.py`

对应 Go repo：

- `cmd/youtu-retriever/`
- `internal/retrieval/`
- `internal/dataset/`
- `internal/chunks/`
- `internal/sidecar/`

能力：

- chunk FAISS retrieval。
- node / relation FAISS retrieval。
- path1：先找相关节点和关系，再沿图扩展出候选 triples。
- path2：直接搜 triple vector index，再扩展、rescore。
- path1/path2 merge。
- rerank。
- 输出 triples / chunk ids / chunk contents。

这是我们已经做得最深的一块。

### 5. 问题分解

对应 Python 文件：

- `models/retriever/agentic_decomposer.py`

能力：

- 读取 schema。
- 调 LLM 把复杂问题拆成 sub-questions。
- 给出 involved types。

例子：

用户问一个复杂多跳问题，系统先拆成几个小问题，每个小问题单独检索，再合并上下文。

这部分目前还在 Python。它依赖 LLM，不急着迁 Go。Go service 可以先把它当作一个 Python worker capability。

### 6. 答案生成和 Agent 推理

对应 Python 文件：

- `main.py`
- `backend.py`
- `utils/call_llm_api.py`

能力：

- no-agent：问题分解 -> 检索 -> 组织 prompt -> 生成答案。
- agent：先做一次 no-agent 分析，然后进入 IRCoT 循环。
- IRCoT 循环里，LLM 会判断：
  - 现在信息够不够回答？
  - 如果不够，需要补查什么新 query？
- 新 query 会再次触发 retrieval。

这部分是“问答体验”的核心，不只是 fuzzy search。

### 7. Web 后端、进度、可视化、评估

对应 Python 文件：

- `backend.py`
- `frontend/index.html`
- `utils/eval.py`

能力：

- FastAPI 后端。
- WebSocket 进度。
- graph visualization。
- subquery visualization。
- reasoning flow visualization。
- LLM judge 评估答案。

这部分说明 Youtu-RAG 已经不是一个单纯脚本，它有产品服务雏形。但现在这个服务是 Python monolith，不适合作为长期工程边界。

## 总体工程方向

目标不是：

> 把 Python 每一行都翻成 Go。

目标应该是：

> 用 Go 做长期稳定服务，把 Python 中模型重、研究性强、变化快的部分拆成清晰 sidecar/worker。

Go service 负责：

- HTTP API。
- dataset registry。
- artifact registry。
- job lifecycle。
- sidecar lifecycle。
- request validation。
- status / health / metrics。
- logs / progress events。
- retry / timeout / cancellation。
- 权限和未来多用户隔离。
- 稳定的 response contract。

Python worker 负责：

- document parser 中比较麻烦的格式解析。
- graph construction。
- TreeComm。
- LLM decomposition。
- embedding。
- FAISS。
- rerank。
- answer generation。
- eval judge。

后面如果某一块 Python worker 已经非常稳定，而且 Go 迁移收益明确，再单独迁。

## 推荐目标架构

```text
                         ┌──────────────────────────┐
                         │        Client / UI         │
                         └─────────────┬────────────┘
                                       │
                                       ▼
                         ┌──────────────────────────┐
                         │      Go Youtu-RAG API     │
                         │  auth / api / jobs / logs │
                         └─────────────┬────────────┘
                                       │
         ┌─────────────────────────────┼─────────────────────────────┐
         │                             │                             │
         ▼                             ▼                             ▼
┌──────────────────┐        ┌──────────────────┐          ┌──────────────────┐
│ Dataset Registry │        │   Job Manager    │          │ Artifact Manager │
│ corpus/schema    │        │ construct/ask    │          │ graph/chunk/cache│
└────────┬─────────┘        └────────┬─────────┘          └────────┬─────────┘
         │                           │                             │
         └───────────────────────────┼─────────────────────────────┘
                                     │
                                     ▼
                      ┌──────────────────────────────┐
                      │ Python Workers / Sidecars     │
                      │ construct / LLM / FAISS / NLP │
                      └──────────────────────────────┘
```

这里的关键变化是：

- Python 不再直接当“主后端”。
- Go 是系统入口和流程负责人。
- Python 是被 Go 管理、调用、健康检查的能力提供方。

## 分阶段推进计划

### Phase S1：Go Service Skeleton

目标：

先把 Go 从 CLI 升级成长期运行的服务。

要做：

- 新增 `cmd/youtu-rag-service/`。
- 加 HTTP server。
- 加基础配置。
- 加 graceful shutdown。
- 加 health endpoint。
- 加 service version endpoint。
- 保留现有 CLI，不破坏 `cmd/youtu-retriever`。

建议 endpoint：

```text
GET  /healthz
GET  /readyz
GET  /v1/version
GET  /v1/datasets
POST /v1/retrieve
```

`POST /v1/retrieve` 先复用现有 `RetrieveRequest` / `RetrieveResult`。

验收：

- `go test ./...` 通过。
- `go run ./cmd/youtu-rag-service` 可以启动。
- `GET /healthz` 返回 ok。
- `POST /v1/retrieve` 在 demo artifact 下能跑通当前 `native-path1-rerank`。
- 现有 `make demo-gates` 不回退。

为什么先做这个：

因为没有长期服务入口，后面的 dataset、job、artifact 都只能散落在脚本里。

### Phase S2：Dataset Registry

目标：

Go service 先知道“有哪些 dataset”和“每个 dataset 的 artifact 是否准备好”。

要做：

- 定义 dataset metadata。
- 支持 demo dataset。
- 支持 uploaded dataset。
- 扫描或登记以下 artifact：
  - corpus
  - schema
  - graph
  - chunks
  - FAISS cache
  - golden / trace 可选
- 给每个 dataset 一个 status。

建议 endpoint：

```text
GET  /v1/datasets
GET  /v1/datasets/{dataset}
GET  /v1/datasets/{dataset}/artifacts
```

Dataset 状态可以先简单分成：

```text
empty
corpus_ready
schema_ready
graph_ready
retrieval_ready
error
```

验收：

- demo dataset 能被发现。
- 缺 graph/chunks/cache 时状态能说明缺什么。
- clean clone 下路径不正确时给出明确错误，不静默失败。

为什么重要：

长期服务不能靠“目录刚好存在”。它必须知道自己有哪些材料、哪些可用、哪些坏了。

### Phase S3：Artifact Manager

目标：

把文件路径和 artifact 读写从业务逻辑里抽出来。

要做：

- 建立 artifact layout 约定。
- 把 graph/chunks/schema/cache/golden/trace 路径统一由 Artifact Manager 解析。
- 支持绝对路径配置和 workspace-relative 配置。
- 对 artifact 做存在性、版本、schema 检查。

建议目录抽象：

```text
data/
  datasets/<dataset>/corpus.json
  schemas/<dataset>.json
  graphs/<dataset>_new.json
  chunks/<dataset>.txt
  cache/<dataset>/
  runs/<job_id>/
```

这不一定要求马上移动 `youtu-graphrag` 的现有目录，第一版可以做 mapping。

验收：

- 现有 demo artifact 能通过 mapping 使用。
- 新 service 不直接到处拼字符串路径。
- 错误信息能说清楚缺的是 graph、chunks、schema 还是 cache。

### Phase S4：Python Sidecar Lifecycle

目标：

Go service 不只是“假设 sidecar 已经启动”，而是能管理和检查 sidecar。

第一版可以不自动拉起 Python，但至少要做到：

- 配置 sidecar URL。
- `GET /readyz` 检查 sidecar health。
- 检查 sidecar schema versions。
- 检查 demo cache 是否可用。
- timeout 和 error 统一包装。

建议 endpoint：

```text
GET /v1/sidecars
GET /v1/sidecars/vector/health
```

后续可以支持：

- Go service 启动时自动拉起 Python sidecar。
- sidecar crashed 后重启。
- sidecar logs 汇总。

验收：

- sidecar 未启动时，service 返回明确 `sidecar_unavailable`。
- sidecar schema version 不匹配时，返回明确 contract error。
- sidecar 正常时，`readyz` 才返回 ready。

### Phase S5：Async Job Manager

目标：

把图构建、重建、问答 agent loop 这类长任务变成 job。

为什么：

- graph construction 很慢。
- LLM 调用会失败。
- 用户需要看到进度。
- 服务需要支持重试、取消、恢复。

建议 job 类型：

```text
construct_graph
reconstruct_graph
retrieve
ask_question
evaluate
```

建议 endpoint：

```text
POST /v1/jobs
GET  /v1/jobs/{job_id}
GET  /v1/jobs/{job_id}/events
POST /v1/jobs/{job_id}/cancel
```

第一版 job store 可以是内存。

第二版再换 SQLite / Badger / Postgres。

验收：

- 提交一个 job 后立即返回 `job_id`。
- 可以轮询 status。
- 可以看到 progress events。
- job 失败时能看到失败阶段和错误。

### Phase S6：Graph Construction Worker Boundary

目标：

先不要把 `KTBuilder` 翻成 Go，而是把 graph construction 变成 Go 调度的 Python worker。

要做：

- 给 Python 侧提供一个 construction worker entry。
- Go 提交 construction job。
- Python worker 读取 corpus/schema，输出 graph/chunks/cache。
- Go 记录 artifact 状态。

建议 Python worker contract：

```text
POST /v1/construction/build
{
  "dataset": "demo",
  "corpus_path": "...",
  "schema_path": "...",
  "output_graph_path": "...",
  "output_chunks_path": "...",
  "mode": "agent"
}
```

返回：

```text
{
  "schema_version": "construction/v1",
  "dataset": "demo",
  "graph_path": "...",
  "chunks_path": "...",
  "stats": {
    "documents": 1,
    "chunks": 3,
    "nodes": 120,
    "edges": 240
  }
}
```

验收：

- Go service 触发 demo graph construction。
- construction 完成后 dataset 状态变成 `retrieval_ready`。
- 构建期间可以查 progress。
- 构建失败时不污染旧 artifact，或者能标出 failed run。

### Phase S7：Ask Question Service

目标：

把 `backend.py` 里的 `/api/ask-question` 迁成 Go 主控流程。

第一版不要迁 LLM 逻辑，只迁编排：

1. Go 接收用户问题。
2. Go 调 Python decomposition worker。
3. Go 对每个 sub-question 调当前 retrieval engine。
4. Go 合并 triples/chunks。
5. Go 调 Python answer generation worker。
6. Go 返回 answer + retrieved context + debug。

建议 endpoint：

```text
POST /v1/ask
```

请求：

```json
{
  "dataset": "demo",
  "question": "...",
  "mode": "noagent"
}
```

响应：

```json
{
  "answer": "...",
  "sub_questions": [],
  "retrieval": {},
  "reasoning_steps": [],
  "debug": {}
}
```

验收：

- demo 问题能通过 Go service 完成完整 QA。
- retrieval 部分继续通过现有 golden gate。
- answer generation 不要求 byte-for-byte 一致，但要记录 prompt/context/debug。

### Phase S8：Agent / IRCoT Workflow

目标：

把 Python `agent_retrieval` / backend iterative reasoning 变成 Go 管理的 workflow。

核心流程：

```text
initial question
  -> decompose
  -> retrieve
  -> generate initial answer
  -> loop:
       LLM decides final answer or new query
       if new query: retrieve more
       update context
  -> final answer
```

Go 应该负责：

- step 状态。
- max steps。
- progress events。
- 每一步输入输出记录。
- cancellation。
- timeout。

Python 继续负责：

- LLM response。
- decomposition。
- answer generation prompt。

验收：

- demo agent run 能输出 reasoning steps。
- 可以看到每一步用了什么 query、拿到多少 triples/chunks。
- WebSocket 或 SSE 能流式返回进度。

### Phase S9：Visualization API

目标：

把 `backend.py` 中的 visualization payload 变成稳定 API。

建议 endpoint：

```text
GET /v1/datasets/{dataset}/graph
GET /v1/jobs/{job_id}/visualization
```

返回：

- graph nodes/edges。
- retrieved subgraph。
- subquery breakdown。
- reasoning flow。

这部分先不重写前端也可以，只要 API 稳定。

验收：

- 能返回 demo graph visualization JSON。
- 能返回一次 ask job 的 reasoning visualization JSON。
- 数据结构有 schema 或文档。

### Phase S10：Production Hardening

目标：

把服务变成能长期运行的东西。

要做：

- Docker Compose。
- Go service + Python sidecar 启动脚本。
- structured logs。
- metrics。
- request id / job id。
- config file。
- artifact cleanup。
- cache rebuild。
- concurrency limit。
- rate limit。
- timeout。
- smoke test。

验收：

- 一条命令启动完整 demo 服务。
- 一条命令跑 end-to-end smoke。
- sidecar 没启动、LLM key 缺失、artifact 缺失，都有可读错误。

## 建议任务拆分

下一轮可以先拆 3 个并行任务。

### Task A：Go Service Skeleton

Owner：Go 主线。

交付：

- `cmd/youtu-rag-service`
- `/healthz`
- `/readyz`
- `/v1/version`
- `/v1/retrieve`
- README service 启动说明

验收：

- `go test ./...`
- service 启动成功
- demo retrieve 通过当前 gate

### Task B：Dataset / Artifact Contract

Owner：可以由 Python/contract 侧协作。

交付：

- dataset registry 文档
- artifact layout 文档
- demo dataset mapping
- error code 列表

验收：

- Go service 能报告 demo dataset 的 artifact 状态
- 缺文件时错误明确

### Task C：Service Smoke Gate

Owner：测试侧。

交付：

- service smoke script
- retrieve API regression gate
- sidecar unavailable gate

验收：

- sidecar 正常：`POST /v1/retrieve` 通过
- sidecar 停止：返回明确错误
- artifact path 错误：返回明确错误

## 先不做什么

为了工程收益最大化，以下事情先不要做：

### 不急着把 graph construction 翻成 Go

原因：

- 它依赖 LLM prompt。
- 它依赖 schema evolution。
- 它涉及 TreeComm。
- 研究变化会比较快。

更好的做法：

- 先把它变成 Go 调度的 Python worker。

### 不急着把 answer generation 翻成 Go

原因：

- 本质上是 prompt + LLM call。
- Go 调 LLM 当然可以，但收益不如先把 workflow 管起来。

更好的做法：

- Go 先记录 prompt、context、response、step。
- 后面如果需要，再把 LLM client 抽到 Go。

### 不急着重写前端

原因：

- 现在核心风险在服务边界和 artifact/job 管理。
- 前端可等 API 稳定后再做。

更好的做法：

- 先把 API 和 visualization JSON 稳定下来。

### 不急着 full-Go FAISS / embedding

原因：

- 现在 Python sidecar 已经能稳定提供 embedding、FAISS、rerank。
- Go 原生替换的工程收益暂时不如服务化。

更好的做法：

- 保留 sidecar。
- 强化 schema/version/health/timeout。

## 推荐的近期里程碑

我建议近期只追三个里程碑。

### Milestone 1：Go Service 能长期跑

标准：

- 有 `cmd/youtu-rag-service`。
- 有 health/ready。
- 有 `/v1/retrieve`。
- 能复用当前 retrieval engine。
- demo gate 不回退。

### Milestone 2：Go Service 能管理 dataset/artifact

标准：

- 能列出 dataset。
- 能判断 graph/chunks/schema/cache 是否 ready。
- 能给出清楚错误。
- 不再到处手写路径。

### Milestone 3：Go Service 能跑完整 no-agent QA

标准：

- Go 接受问题。
- Python 负责 decomposition。
- Go 调 retrieval。
- Python 负责 answer generation。
- Go 返回 answer、sub_questions、retrieval debug。

完成这三个后，Youtu-RAG 就从“脚本 + demo backend + retriever CLI”变成了一个真正的长期服务雏形。

## 成功标准

这条路线的成功，不是“Python 消失”。

成功标准应该是：

- 用户只和 Go service 交互。
- 所有任务有 job id。
- 所有 artifact 有状态。
- 所有 sidecar 有 health。
- 所有重要路径有 gate。
- Python 变成可替换 worker，而不是主控系统。
- 以后要迁 Go 的时候，是一块一块替换 worker，不影响外部 API。

也就是说：

> 先把系统变成一个稳定服务，再考虑哪些 worker 值得继续 Go 化。

