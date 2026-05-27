# Dataset Benchmark Guide

这份文档回答一个很具体的问题：如果现在想拿 Youtu-RAG 做一次数据集
benchmark，应该准备什么、跑哪条命令、看哪些指标，以及我们这个 Go
service 目前还差哪一层自动化。

先说结论：

- 原 Python `youtu-graphrag` repo 已经有完整的“构图 -> 检索 -> 回答 ->
  LLM judge 评分”主流程，入口是 `main.py`。
- 这个 Go repo 现在已经能稳定 service 化很多能力：dataset import,
  parse/build/answer jobs, workflow, schema, operations, retrieve parity,
  smoke/release gates。
- 但“对一个 benchmark QA 文件批量跑 N 道题、自动汇总 accuracy/latency/cost”
  还没有被做成 Go service 的一键 job/workflow。
- 所以今天要立刻跑论文式 benchmark，优先用原 Python repo；要把它变成长期
  服务能力，下一步应该加 `benchmark` job/workflow。

## 1. Benchmark 到底测什么

不要把所有测试都叫 benchmark。这里建议分四层。

### Level 0: 服务能不能启动

这是工程 smoke，不是模型效果评测。

```bash
make release-check
make service-smoke
```

它证明 OpenAPI、脚本、配置、fake service smoke 没坏。

### Level 1: demo 检索是否和 Python golden 对齐

这是 retrieval regression，也不是完整数据集 benchmark。

```bash
YOUTU_RAG_ARTIFACT_ROOT=/abs/path/youtu-graphrag make demo-service-smoke
```

它会启动真实 Python vector sidecar 和 Go service，然后检查 demo 问题的
`loader/chunk/triple/full` 是否和 Python golden 0 diff。

适合回答：“我们迁 Go/service 化有没有把检索链路弄坏？”

### Level 2: 原 Python repo 的论文式 benchmark

这是现在最接近论文实验口径的路径。

原 repo 的 `main.py` 会：

1. 读取 `config/base_config.yaml` 里的 dataset 配置。
2. 可选构建 graph/chunks/cache。
3. 对 QA 文件里的每道题做 decomposition/retrieval/answer。
4. 用 `utils/eval.py` 里的 LLM judge 判断预测答案和 gold answer 是否一致。
5. 统计 accuracy 和平均耗时。

适合回答：“Youtu-RAG 在 HotpotQA / 2Wiki / MuSiQue / GraphRAG-Bench /
AnonyRAG 这种数据集上效果怎么样？”

Phase 33 后，论文式 benchmark 的工程合同单独固化在
`docs/contracts/paper_aligned_benchmark.md`。它要求复用原 Python
`GraphQ` + `KTRetriever` + `Eval` 链路，并用外层 runner 增加 checkpoint、
progress、timeout 和失败预算。它不是 `corpus_keyword_overlap` smoke，也不是只
调用 Go service `/v1/retrieve` 的轻量 answer/judge。

### Level 3: Go service 化 benchmark

这是我们还没完全做完的一层。

理想形态是：

1. `POST /v1/jobs` 或 `POST /v1/workflows` 提交 `benchmark`。
2. 输入 dataset、QA 文件、model、judge model、limit、并发度。
3. service 自动调度 retrieve/answer/judge。
4. 每道题都留下 job/operation 记录。
5. 输出一个 `benchmark-result/v1` artifact。
6. 对长 benchmark 输出 progress/checkpoint，支持小并发和失败可恢复。

这层从 Phase 26 开始服务化：benchmark job/workflow 的稳定合同在
`docs/contracts/benchmark_worker.md`。如果目标是严格贴近论文 retrieval /
answer / eval 链路，还要看 Phase 33 的
`docs/contracts/paper_aligned_benchmark.md`。

## 2. 官方可用的数据集线索

论文附件和公开页面里能确认的线索：

- Paper: <https://arxiv.org/abs/2508.19855>
- Official repo: <https://github.com/TencentCloudADP/youtu-graphrag>
- AnonyRAG dataset: <https://huggingface.co/datasets/Youtu-Graph/AnonyRAG>

Hugging Face 的 AnonyRAG 页面显示它是 question answering/text 数据集，
包含英文和中文子集，并且有 QA 和 Texts 四个 subset：

- `AnnoyRAG-CHS-QA`
- `AnnoyRAG-CHS-Texts`
- `AnnoyRAG-ENG-QA`
- `AnnoyRAG-ENG-Texts`

原 Python repo 的 `config/base_config.yaml` 里已经预置了这些 dataset key：

| dataset key | corpus | QA | schema |
| --- | --- | --- | --- |
| `demo` | `data/demo/demo_corpus.json` | `data/demo/demo.json` | `schemas/demo.json` |
| `hotpot` | `data/hotpotqa/hotpotqa_corpus.json` | `data/hotpotqa/hotpotqa.json` | `schemas/hotpot.json` |
| `2wiki` | `data/2wiki/2wikimultihopqa_corpus.json` | `data/2wiki/2wikimultihopqa.json` | `schemas/2wiki.json` |
| `musique` | `data/musique/musique_corpus.json` | `data/musique/musique.json` | `schemas/musique.json` |
| `graphrag-bench` | `data/graphrag-bench-reformat/bench_corpus.json` | `data/graphrag-bench-reformat/graphrag-bench.json` | `schemas/graphrag-bench.json` |
| `anony_chs` | `data/anony_chs/final_chunk_corpus.json` | `data/anony_chs/final_qa_pairs.json` | `schemas/anony_chs.json` |
| `anony_eng` | `data/anony_eng/final_chunk_corpus.json` | `data/anony_eng/final_qa_pairs.json` | `schemas/anony_eng.json` |

注意：config 里有路径，不代表本机已经下载了所有数据。没有数据文件时，
需要先按论文/repo/Hugging Face 的说明放到这些路径。

### 2.1 Prepare AnonyRAG Locally

This repo includes a helper that downloads the public Hugging Face parquet
files and converts them into the original `youtu-graphrag` JSON layout:

```bash
cd /abs/path/youtu-rag-service

python3 scripts/prepare_anonyrag.py \
  --artifact-root /abs/path/youtu-graphrag
```

It writes:

```text
youtu-graphrag/data/anonyrag/raw/
  annoyrag_chs_qa.parquet
  annoyrag_chs_text_chunks.parquet
  annoyrag_eng_qa.parquet
  annoyrag_eng_text_chunks.parquet

youtu-graphrag/data/anony_chs/
  final_qa_pairs.json
  final_qa_pairs.sample20.json
  final_chunk_corpus.json
  final_chunk_corpus.sample200.json

youtu-graphrag/data/anony_eng/
  final_qa_pairs.json
  final_qa_pairs.sample20.json
  final_chunk_corpus.json
  final_chunk_corpus.sample200.json

youtu-graphrag/schemas/anony_chs.json
youtu-graphrag/schemas/anony_eng.json
```

On the current local machine, this produced:

| dataset | QA rows | corpus chunks |
| --- | ---: | ---: |
| `anony_chs` | 688 | 2763 |
| `anony_eng` | 709 | 3447 |

The generated schemas are starter schemas for benchmark smoke/build
experiments. They are intentionally conservative; for paper-grade evaluation,
replace them with the official schema if the paper/repo releases a stricter
one.

## 3. 数据要准备成什么样

最小需要三类文件。

### 3.1 corpus

用于构图。原 repo 期望每个 dataset 在 `config/base_config.yaml` 里有一个
`corpus_path`。

示例：

```yaml
datasets:
  hotpot:
    corpus_path: data/hotpotqa/hotpotqa_corpus.json
```

如果是原始 PDF/doc/txt/md，不能直接当 benchmark corpus。先用当前 service 的
`parse_documents` 或原 repo parser 转成 corpus JSON，再 import/build。

### 3.2 schema

用于指导图构建，路径是 `schema_path`。

示例：

```yaml
datasets:
  hotpot:
    schema_path: schemas/hotpot.json
```

当前结论是：Youtu-RAG 不是完全“零 schema 自动生成”。它主要依赖预置 schema
或用户提供 schema；agent mode 有 schema evolution 逻辑，但那更像构图过程中的
增量扩展，不应该当作稳定的从零 schema generator。

### 3.3 QA

用于 benchmark 问答，路径是 `qa_path`。

示例：

```yaml
datasets:
  hotpot:
    qa_path: data/hotpotqa/hotpotqa.json
```

每条 QA 至少要能提供：

- `question`
- `answer`

原 repo 的 evaluator 会把预测答案和 gold answer 交给 LLM judge，要求 judge
只返回 `1` 或 `0`。

## 4. 立即跑一次 benchmark 的推荐流程

### Step 1: 固定模型环境

用 DeepSeek 时，建议显式设置：

```bash
export LLM_API_KEY="${DEEPSEEK_API_KEY}"
export LLM_BASE_URL="https://api.deepseek.com"
export LLM_MODEL="deepseek-v4-pro"
```

不要把 key 写进 repo、日志或 artifact。只记录 model id 和 base URL。

### Step 2: 先跑 demo，确认环境没问题

在 Go service repo：

```bash
cd /abs/path/youtu-rag-service

YOUTU_RAG_ARTIFACT_ROOT=/abs/path/youtu-graphrag \
YOUTU_RAG_HTTP_ADDR=127.0.0.1:18082 \
YOUTU_RAG_SIDECAR_URL=http://127.0.0.1:18765 \
make demo-service-smoke
```

这一步验证真实 sidecar + Go service + demo retrieve。它不需要 LLM key。

如果要验证 DeepSeek answer，先用很小的 demo 问题跑，不要直接上全量数据集。

### Step 3: 选择一个小数据集或子集

建议顺序：

1. `demo`
2. `anony_eng` 或 `anony_chs` 的小 subset
3. `hotpot`
4. `2wiki`
5. `musique`
6. `graphrag-bench`

第一次不要全量跑。先抽 10 到 20 条 QA，确认：

- 构图能完成；
- retrieval 不报错；
- answer 能生成；
- judge 能返回 `1/0`；
- 日志里能看到 accuracy。

### Step 4: 用原 Python repo 跑论文式流程

在原 Python repo：

```bash
cd /abs/path/youtu-graphrag
. .venv/bin/activate

export LLM_API_KEY="${DEEPSEEK_API_KEY}"
export LLM_BASE_URL="https://api.deepseek.com"
export LLM_MODEL="deepseek-v4-pro"

python main.py \
  --config config/base_config.yaml \
  --datasets demo \
  --override '{"triggers":{"constructor_trigger":false,"retrieve_trigger":true,"mode":"noagent"}}'
```

如果 graph/chunks/cache 还没构建，则把 `constructor_trigger` 改为 `true`：

```bash
python main.py \
  --config config/base_config.yaml \
  --datasets demo \
  --override '{"triggers":{"constructor_trigger":true,"retrieve_trigger":true,"mode":"noagent"}}'
```

`noagent` 更适合第一轮 smoke，因为它少走 agent/IRCoT 分支，比较快。环境稳定后
再跑 `mode=agent`。

### Step 5: 扩到正式数据集

示例：

```bash
python main.py \
  --config config/base_config.yaml \
  --datasets hotpot 2wiki musique graphrag-bench \
  --override '{"triggers":{"constructor_trigger":true,"retrieve_trigger":true,"mode":"agent"}}'
```

全量 benchmark 可能会很慢、很贵。建议先在 QA 文件层面裁剪小样本，或者新增
一个 limit 参数/脚本做抽样。

## 5. 应该记录哪些结果

最少记录这些：

| 类型 | 字段 |
| --- | --- |
| run metadata | dataset, split, question count, timestamp, git commit |
| model config | answer model, judge model, base URL, temperature |
| graph config | construction mode, schema path, corpus path |
| retrieval config | top_k, recall_paths, rerank flags |
| answer metrics | correct count, accuracy, failed count |
| AnonyRAG mapping metrics | mapping precision, recall, F1, exact recall |
| system metrics | total time, average time/question, worker errors |
| artifacts | graph path, chunks path, answer output, logs |

不要只记录最后一个 accuracy。benchmark 复现最怕缺配置。

AnonyRAG 的任务是恢复 `PERSON#xxx`、`LOCATION#xxx` 这类匿名实体，所以只看
LLM judge 的 `accuracy` 不够稳。当前 service worker 会额外从 gold/predicted
answer 里抽取匿名映射，输出 `anonymized_mapping` 聚合指标和每题
`mapping_score`：

- `precision`：预测出来的映射里有多少是对的；
- `recall`：gold 映射里有多少被找回；
- `f1`：precision/recall 的综合；
- `exact_recall`：严格字符串完全一致的找回率。

这组指标不会替代 LLM judge，但在评估匿名实体恢复质量时应该一起看。比如模型
回答 `洪信（即洪太尉）`，LLM judge 可能判对；deterministic scorer 会在 relaxed
`matched_count` 里算对，同时 strict `exact_recall` 会提醒它没有完全按 gold 名称输出。

为了让 strict 指标更有意义，worker 对 AnonyRAG 映射题做了两个约束：

- 首答 prompt 要求只输出 `ID——实体` 映射行；
- 首答后会做一次不看 gold 的 format repair，只把问题、上下文、首答和 required
  anonymous IDs 给模型，让它补齐并改成稳定映射格式。

最终结果里 `answer_repaired=true` 表示 `predicted_answer` 是 repair 后的答案，
`original_predicted_answer` 保留首答，便于排查 repair 是否把语义改坏。

## 6. 用 Go service 做 benchmark 时目前能做到什么

当前 Go service 已经具备这些长期服务能力：

- dataset import/schema management；
- parse_documents/build_graph/answer/retrieve/generate_golden jobs；
- create_dataset/build_and_answer workflows；
- operation history；
- workflow/job events；
- artifact metadata；
- service profile validation；
- real demo service smoke。

所以它已经能支撑“服务化 benchmark”的底层能力。

Phase 26 后它已经有第一版 `benchmark` job/workflow runner，并提供了一个
小样本真实模型 smoke：

```bash
export LLM_API_KEY="${DEEPSEEK_API_KEY}"
export LLM_BASE_URL="https://api.deepseek.com"
export LLM_MODEL="deepseek-v4-pro"
YOUTU_RAG_ARTIFACT_ROOT=/abs/path/youtu-graphrag make benchmark-smoke
```

默认跑 `anony_eng` 的 1 条 QA，通过 Go HTTP service 提交 `benchmark` job，
由 Python worker 写出 `benchmark-result/v1`。这条命令是 smoke，不是论文
全量跑分；全量 benchmark 需要显式调大 `BENCHMARK_LIMIT` 并关注 token 成本。

### Phase 33: paper-aligned AnonyRAG run

如果要按论文方法测 `anony_chs`，建议固定这些已构好的 artifact：

```text
QA:     youtu-graphrag/data/anony_chs/final_qa_pairs.json
graph:  youtu-graphrag/output/graphs/anony_chs_full_flash_community.json
chunks: youtu-graphrag/output/chunks/anony_chs_full_flash_community.txt
schema: youtu-graphrag/schemas/anony_chs.json
wal:    youtu-graphrag/output/graph_wal/anony_chs_full_flash_community.compaction.jsonl
```

这条路径应使用原 repo 的 `GraphQ` decomposition、`KTRetriever`
retrieval/answer，以及 `utils/eval.py` 的 LLM judge。主方法用 `mode=agent`；
`mode=noagent` 只能作为 ablation 单独记录。当前建议的工程口径是
`prompt_mode=open`、`community_compaction=completed`、DeepSeek V4 Flash/Pro、
`retry_failed=true`。它不是锁论文旧模型的复现，而是使用论文
retrieval/answer/eval 链路加工业化 WAL / multi-runner / replay-only community
compaction runtime 的方法评测。

全量跑之前先做 no-model preflight：

```bash
YOUTU_RAG_ARTIFACT_ROOT=/abs/path/youtu-graphrag \
PAPER_BENCHMARK_LIMIT=688 \
PAPER_BENCHMARK_COMMUNITY=completed \
PAPER_BENCHMARK_PROMPT_MODE=open \
PAPER_BENCHMARK_PREFLIGHT_ONLY=true \
scripts/run_paper_benchmark_smoke.sh
```

正式跑时去掉 `PAPER_BENCHMARK_PREFLIGHT_ONLY` 即可；如果中途网络或 provider
失败，保留同一 checkpoint 并用默认 `PAPER_BENCHMARK_RETRY_FAILED=true`
续跑，只会重试失败项。

长跑可以用多进程分片提速：

```bash
YOUTU_RAG_ARTIFACT_ROOT=/abs/path/youtu-graphrag \
PAPER_BENCHMARK_LIMIT=688 \
PAPER_BENCHMARK_COMMUNITY=completed \
PAPER_BENCHMARK_PROMPT_MODE=open \
PAPER_BENCHMARK_SHARDS=4 \
scripts/run_paper_benchmark_smoke.sh
```

`PAPER_BENCHMARK_SHARDS>1` 会启动多个独立 Python worker，每个 worker
处理一段 QA range，并写自己的 `.shard-XX-of-NN` checkpoint/progress/result。
parent 会先把主 checkpoint 里已完成的题分发到对应 shard，再 merge 回主
result/checkpoint。已有的 `226/688` checkpoint 不需要删除，续跑不会重烧已完成
题。

### Current full AnonyRAG-CHS reference result

The current full-run reference artifact is:

```text
youtu-graphrag/output/benchmarks/anony_chs_deepseek-v4-flash_method_agent_open_completed_limit688.json
```

It is a paper-method-aligned industrial run:

| Field | Value |
| --- | --- |
| dataset | `anony_chs` |
| questions | `688/688` completed |
| mode | `agent` |
| prompt mode | `open` |
| retrieval config | `recall_paths=2`, `top_k_filter=20`, high recall on, rerank on |
| graph source | industrial WAL full graph |
| community compaction | `completed` |
| answer model | `deepseek-v4-flash` |
| judge model | `deepseek-v4-flash` |
| shards | `4` |
| failures | `0` |
| accuracy | `371/688 = 0.5392441860` |

AnonyRAG-specific mapping diagnostics:

| Metric | Value |
| --- | --- |
| applicable questions | `575` |
| expected mappings | `2710` |
| predicted mappings | `2700` |
| matched mappings | `2077` |
| exact matched mappings | `863` |
| precision | `0.7692592593` |
| recall | `0.7664206642` |
| F1 | `0.7678373383` |
| exact recall | `0.3184501845` |

The closest paper comparison is Table 1 top-20 Accuracy, Open mode,
AnonyRAG-CHS. The paper reports Youtu-GraphRAG with DeepSeek-V3-0324 at
`42.88%` and Qwen3-32B at `39.24%`. This run reports `53.92%`, which is
`+11.04` absolute points over the paper's DeepSeek-V3-0324 number. Do not
attribute that delta entirely to service engineering: the base model changed to
DeepSeek V4 Flash, and stronger base-model semantic/reasoning ability is likely
the largest contributor.

The mapping metrics above are not reported by the paper and should not be
mixed with the paper's top-k Accuracy table. They are our deterministic
diagnostic for anonymous-entity restoration quality. Accuracy remains the main
paper-comparable metric; mapping precision/recall/F1/exact recall explain
whether wrong binary-judge items were close on entity slots or completely off.

Report this result as:

```text
paper GraphQ/KTRetriever/Eval path + DeepSeek V4 Flash +
industrial WAL/sharded graph + replay-only community compaction
```

It is not a byte-for-byte reproduction of the paper's original graph
construction or model profile.

详细请求、输出、checkpoint 和验收标准见
`docs/contracts/paper_aligned_benchmark.md`。

Phase 26 的目标合同：

```json
{
  "type": "benchmark",
  "benchmark": {
    "dataset": "demo",
    "qa_path": "/abs/path/youtu-graphrag/data/demo/demo.json",
    "limit": 20,
    "mode": "agent",
    "answer_model": "deepseek-v4-pro",
    "judge_model": "deepseek-v4-pro",
    "output_path": "/abs/path/youtu-graphrag/output/benchmarks/demo.json"
  }
}
```

输出 artifact：

```json
{
  "schema_version": "benchmark-result/v1",
  "dataset": "demo",
  "question_count": 20,
  "correct_count": 18,
  "accuracy": 0.9,
  "items": [
    {
      "question": "...",
      "gold_answer": "...",
      "predicted_answer": "...",
      "judge": "1",
      "latency_ms": 1234,
      "job_id": "..."
    }
  ]
}
```

这会把现在的手动 benchmark 变成一个真正长期可维护的 service feature。

## 7. 推荐下一步

如果现在目标是“马上知道效果”，走原 Python `main.py`。

如果目标是“把 benchmark 变成长期服务能力”，建议下一阶段拆：

1. **Phase 26A: Define benchmark job/workflow contract**
   - 固定 request/result artifact/job events/error semantics。
2. **Phase 26B: Implement benchmark runner**
   - Go 负责批量调度；Python/LLM worker 负责 answer 和 judge。
3. **Phase 26C: Validate benchmark gates**
   - 小型 fixture 跑通 success/failure/restart/readback/bad judge output。

这样做的收益是：以后换数据集、换模型、换 judge，都不用临时写脚本，只改 job
spec。

## 8. 论文式检索链路 vs 轻量 smoke

benchmark 结果必须先看 `retrieval.context_source`，否则很容易误读：

- `paper_kt_retriever`：每道题走原 Python `GraphQ` + `KTRetriever` +
  `Eval` 链路。这是 Phase 33 论文口径 benchmark 应该使用的来源。
- `service_retrieve`：每道题先调用 Go service 的 `/v1/retrieve`，例如
  `retrieve_mode=native-path1-rerank`，再把返回的 triples/chunks 交给
  answer/judge。这才是可以和 Youtu-GraphRAG 检索链路对齐讨论的路径。
- `corpus_keyword_overlap`：worker 直接从 `final_chunk_corpus.json` 里按
  关键词重叠挑 chunks。这只能当 service/LLM smoke 或弱 baseline，不能当成
  论文式 GraphRAG/rerank benchmark。

跑 AnonyRAG 正式对照前，应先确认输出里：

```json
{
  "retrieval": {
    "context_source": "service_retrieve",
    "retrieve_mode": "native-path1-rerank"
  }
}
```

并且每个 item 的 `retrieval.context_source` 也是 `service_retrieve`。如果不是，
这轮只说明 LLM/judge 链路能跑，不说明图检索/rerank 效果。

## 9. 长 benchmark 的 progress / concurrency 建议

AnonyRAG 中文集有 688 条 QA，英文集有 709 条 QA。不要一上来全量高并发跑。

建议顺序：

1. `limit=1`：确认 key、worker、judge 输出正常。
2. `limit=10`、`concurrency=1`：确认稳定性和平均耗时。
3. `limit=50`、`concurrency=1-2`：观察失败率、rate limit、成本。
4. 全量：只在 progress/checkpoint 已经可用时跑。

服务化 benchmark 应记录：

- `benchmark_progress` events：completed/total/correct/failed/accuracy_so_far；
- checkpoint JSONL：每道题的 terminal item，支持 resume；
- `max_failures`：避免坏配置烧完整个数据集；
- `rate_limit_rpm`：避免 API provider 429；
- `concurrency`：默认 1，提并发需要显式配置。

详细合同见 `docs/contracts/benchmark_worker.md` 的 progress/concurrency
章节。

## 10. DeepSeek / API key 处理原则

真实模型 benchmark 优先使用 DeepSeek，但 key 只通过环境变量传给 Python
worker：

```bash
export LLM_API_KEY="${DEEPSEEK_API_KEY}"
export LLM_BASE_URL="https://api.deepseek.com"
export LLM_MODEL="deepseek-v4-pro"
```

Go service 的 job spec/result/artifact/event 只能记录 model id、base URL、
question count、accuracy 等非敏感信息；不能记录或打印 key。缺 key 时，job
应该失败成 `benchmark_llm_unconfigured` 这类可诊断错误。
