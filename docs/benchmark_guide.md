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

### Level 3: Go service 化 benchmark

这是我们还没完全做完的一层。

理想形态是：

1. `POST /v1/jobs` 或 `POST /v1/workflows` 提交 `benchmark`。
2. 输入 dataset、QA 文件、model、judge model、limit、并发度。
3. service 自动调度 retrieve/answer/judge。
4. 每道题都留下 job/operation 记录。
5. 输出一个 `benchmark-result/v1` artifact。

这层从 Phase 26 开始服务化：benchmark job/workflow 的稳定合同在
`docs/contracts/benchmark_worker.md`。

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
| system metrics | total time, average time/question, worker errors |
| artifacts | graph path, chunks path, answer output, logs |

不要只记录最后一个 accuracy。benchmark 复现最怕缺配置。

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

## 8. DeepSeek / API key 处理原则

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
