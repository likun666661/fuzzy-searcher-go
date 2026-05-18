# Youtu-RAG Service 总体架构说明

这份文档解释 `youtu-rag-service` 现在到底是什么、它和原来的
`youtu-graphrag` Python 代码是什么关系、哪些部分已经迁到了 Go、哪些
部分还留在 Python，以及为什么现在不一定要继续追求“全 Go”。

目标读者不是只懂代码的人。你可以把它当成给家里老人解释一个“会帮你
找资料的小助手”的说明书：先讲大图，再讲细节。

## 一句话总结

原来的 Python retriever 像一个很大的厨房：买菜、切菜、炒菜、装盘、
试味道全部挤在一个地方。

现在我们把它拆成了两层：

- Go 是前台掌柜：负责接问题、组织流程、决定去哪找、怎么合并结果、
  最后给出统一格式。
- Python 是后厨里的几台专用机器：负责把文字变成向量、查 FAISS 索引、
  给 triples 打相似度分数、做 path2 的向量重排。

也就是说，现在不是 100% Go，但“业务流程”和“工程骨架”已经在 Go 里。
Python 被压缩成了一个比较薄的 ML/vector sidecar。

## 为什么要这样拆

原来的 `enhanced_kt_retriever.py` 很大，里面混了几类事情：

1. 读图和读 chunk。
2. 找候选节点、候选关系、候选 triples。
3. 调 embedding model，把文本变成向量。
4. 查 FAISS 向量索引。
5. 给 triples 重新打分。
6. 合并 path1/path2 的结果。
7. 把结果格式化成对外 JSON。
8. 处理 cache、线程、模型、异常、demo 数据。

如果一口气把所有东西都改成 Go，会很难知道哪里错了。我们采用的策略是：

1. 先把输入输出固定住。
2. 再把 Python 大函数切成小接口。
3. 每迁一块，就用 golden fixture 对齐结果。
4. Go 逐步接管 orchestration，Python 逐步缩成 sidecar。

这就是现在 repo 里的多个 mode 和 regression gate 的来源。

## 生活类比

可以把整个系统想象成“图书馆问答服务”。

用户问：“梅西在国王杯里被拿来比较的那个人，什么时候被巴萨签下？”

系统要做的不是直接猜答案，而是先找资料：

- 图书馆目录：knowledge graph。
- 书页内容：chunks。
- 书页和目录卡片之间的关系：triples。
- 会按意思找相似内容的机器：embedding + FAISS。
- 最后整理结果的人：Go retriever。

Python 以前既是图书管理员，又是目录系统，又是相似度机器。现在 Go 接管
图书管理员的工作，Python 保留“按意思搜索”的机器能力。

## 当前仓库分工

### Go 仓库：`youtu-rag-service`

这个 repo 是新的工程化 retriever。

它负责：

- 加载 graph JSON。
- 加载 chunk 文本。
- 定义稳定的 `RetrieveRequest` / `RetrieveResult`。
- 调 Python sidecar。
- 生成 path1 raw candidates。
- 合并 path1/path2 triples。
- 排序、截断 `top_k`。
- 输出稳定 JSON。
- 跑 demo golden gates，防止行为回退。

主要目录：

```text
cmd/youtu-retriever/          CLI 入口
internal/dataset/             graph JSON loader
internal/chunks/              chunk txt loader
internal/graphtext/           Python-parity node text helper
internal/retrieval/           retrieval contract 和核心流程
internal/sidecar/             Python sidecar HTTP client
scripts/                      golden compare / demo gates
docs/contracts/               contracts 和 schema
```

### Python 仓库：`youtu-graphrag`

这个 repo 里还保留原来的 Python retriever 和 sidecar 实现。

现在 Go 主要依赖 Python 的这些接口：

```text
POST /v1/embed
POST /v1/faiss/search
POST /v1/retrieval/path2-triples
POST /v1/retrieval/rerank-triples
```

它们分别做：

- `/v1/embed`：把问题文本变成向量。
- `/v1/faiss/search`：查 node / relation / chunk 等 FAISS 索引。
- `/v1/retrieval/path2-triples`：做 Python-authoritative path2 expansion
  和 rescore。
- `/v1/retrieval/rerank-triples`：给 Go 传来的 raw triples 打分。

重要的是：当前主路径已经不再调用：

```text
POST /v1/retrieval/path1-triples
```

这说明 path1 raw candidate generation 已经迁到 Go。

## 一个问题进来之后发生什么

以当前主路径 `native-path1-rerank` 为例。

### 第 1 步：用户问题进入 Go

CLI 或服务调用 Go retriever：

```bash
go run ./cmd/youtu-retriever retrieve \
  --graph ../youtu-graphrag/output/graphs/demo_new.json \
  --chunks ../youtu-graphrag/output/chunks/demo.txt \
  --dataset demo \
  --question "When was the person who Messi's goals in Copa del Rey compared to get signed by Barcelona?" \
  --top-k 20 \
  --sidecar-url http://127.0.0.1:8765 \
  --mode native-path1-rerank
```

Go 得到：

- graph 路径
- chunk 路径
- question
- top_k
- sidecar URL
- mode

### 第 2 步：Go 读取 graph 和 chunks

Go 加载两类资料：

- graph：节点和边，像目录卡片。
- chunks：原文片段，像书页内容。

代码位置：

```text
internal/dataset/
internal/chunks/
```

### 第 3 步：Go 找 path1 raw candidates

path1 可以理解为“从问题相关的节点出发，看它周围有哪些关系”。

Go 会：

1. 调 `/v1/embed` 把 question 变成向量。
2. 调 `/v1/faiss/search` 查 node index，找相关节点。
3. 调 `/v1/faiss/search` 查 relation index，找相关关系。
4. 在 Go 里根据 graph 做三类扩展：
   - neighbor expansion：看相关节点附近的一跳关系。
   - path-based search：沿着图走一小段路径。
   - relation-matched search：找命中相关 relation 的边。
5. 去重，得到 raw triples。

当前 demo 下，新路径的 debug meta 是：

```text
path1_schema_version = go-path1-candidates/v1
path1_raw_count = 97
path1_top_nodes_count = 20
path1_top_relations_count = 20
path1_keyword_count = 14
```

这一步以前是 Python `/v1/retrieval/path1-triples` 做的，现在已经是 Go 做。

#### path1 再讲细一点

path1 的思路是“先找点，再沿着点附近找边”。

继续用图书馆类比：如果用户问的是梅西和国王杯，那么 path1 会先在目录里
找到可能相关的卡片，比如：

- Lionel Messi
- Copa del Rey
- FC Barcelona
- Diego Maradona
- 一些比赛、球队、事件、属性节点

找到这些卡片之后，它不会马上输出答案，而是把这些卡片附近的关系都先拿出来
当候选。

path1 具体分三种候选来源。

第一种是 neighbor expansion，也就是“一跳邻居”。

如果图里有：

```text
Lionel Messi -- participates_in --> Gerard Piqué
Lionel Messi -- has_attribute --> defeated: FC Barcelona
Lionel Messi -- comparable_to --> Diego Maradona's goal
```

只要 `Lionel Messi` 是相关节点，这些边就可能被拿出来。

第二种是 path-based search，也就是“沿着路走一小段”。

有时候答案不在相关节点的第一条边上，而是在从这个点走出去的一条短路径里。
path1 会从相关节点出发，最多走几步，看路径上的节点文本是否命中问题里的关键词。
如果命中，就把路径里的边也拿出来当候选。

第三种是 relation-matched search，也就是“按关系词找边”。

如果问题里或者 relation FAISS 里认为 `belongs_to`、`participates_in`、
`comparable_to` 这类关系可能重要，那么 Go 会在 graph 里找这些关系对应的边。
但它不会全图乱拿，要求边的两端至少有一个靠近前面找到的相关节点。

这三种来源会合在一起，然后去重。去重后的结果叫 raw triples。

raw triples 的特点是：

- 它们还没有最终分数。
- 它们只是“候选资料卡片”。
- 它们可能有一些不重要的边。
- 所以后面必须 rerank。

当前 Phase 9 中，这一步已经由 Go 完成。Python 不再负责 path1 candidate
generation。

### 第 4 步：Python 只负责给 path1 candidates 打分

Go 把 raw triples 发给：

```text
POST /v1/retrieval/rerank-triples
```

Python 做的事情很窄：

1. 把每条 triple 转成文本。
2. 用 embedding model 算相似度。
3. 返回带 score 的 triples。

这一步像“试味机器”：Go 把候选菜端过去，Python 只负责给每道菜打分。

当前 demo 下：

```text
rerank_input_count = 97
rerank_count = 97
```

### 第 5 步：Python 还负责 path2 vector rescore

path2 可以理解为“直接从 triple 向量索引里找相关 triples，再扩展和重排”。

Go 调：

```text
POST /v1/retrieval/path2-triples
```

Python 返回 `rescored_triples`。

当前 demo 下：

```text
path2_count = 20
```

#### path2 再讲细一点

path2 和 path1 最大的区别是：path1 先找节点，path2 先找 triple。

还是图书馆类比：

- path1 像先找“梅西”这张人物卡，再看这张卡旁边连着哪些资料。
- path2 像直接在“所有关系句子”的索引里搜，看哪几句关系句子本身最像问题。

所谓 triple，可以理解成一条关系句子：

```text
头节点 -- 关系 -- 尾节点
```

比如：

```text
Lionel Messi -- comparable_to -- Diego Maradona's goal
Copa del Rey -- has_attribute -- achievement: first club in Spain to win the treble
Jesús Aranguren -- participates_in -- Copa del Rey
```

Python sidecar 里有一个 triple 向量索引。它会把每条 triple 也变成向量。
用户问题来了之后，path2 会做几件事：

1. 把问题变成 query vector。
2. 去 triple FAISS index 里查最像这个问题的 triples。
3. 对查到的 triples 做 expansion。
4. 对 expansion 后的候选重新计算分数。
5. 返回分数排好序的 `rescored_triples`。

这里的 expansion 很重要。它不是只拿 FAISS 命中的那几条 triple，而是会看命中
triple 的头尾节点附近还有哪些相关 triple。原因是：

如果只看一条命中的 triple，可能资料太窄；扩展一下邻居，可能会找到真正回答
问题需要的上下文。

所以 path2 更像“直接从关系句子索引里找线索，然后顺着线索附近再捞一圈”。

这就是为什么 path2 现在还留在 Python：它不只是一个简单的图邻居查询，还绑定了
triple FAISS index、向量重排、候选扩展、threshold filter、cache 等细节。

当前 Go 拿到的是 Python 已经处理好的：

```text
path2-triples/v1.rescored_triples
```

Go 不需要知道 Python 内部怎么算的，只要按 contract 合并它。

### 第 6 步：Go 合并 path1 和 path2

Go 拿到两批结果：

- path1 reranked triples
- path2 rescored triples

然后 Go 做：

1. path2 放前面。
2. path1 放后面。
3. 按 score 降序排序。
4. 截断到 `top_k`。
5. 过滤不该对外展示的关系。
6. 收集相关 chunk ids。
7. 拼出最终 `RetrieveResult`。

这部分已经在 Go。

#### 为什么需要 path1 和 path2 两路

因为它们擅长的事情不一样。

path1 擅长“从相关实体附近找资料”。如果问题明确提到一个人、球队、比赛，
path1 很适合从这些节点往外扩。

path2 擅长“直接找语义相似的关系句子”。如果某条 triple 本身和问题很像，
path2 可以直接命中，即使它不在最明显的相关节点旁边。

两路合起来更稳：

- path1 提供图结构附近的召回。
- path2 提供向量语义上的召回。
- rerank/rescore 给它们分数。
- Go 最后统一排序。

可以把它想成两个人找资料：

- 第一个人按目录卡片找。
- 第二个人按句子意思找。
- 最后让 Go 这个整理员把两个人找到的资料合并，按可信度排好。

### 第 7 步：Go 输出稳定 JSON

最终输出长这样：

```json
{
  "triples": [],
  "chunk_ids": [],
  "chunk_contents": [],
  "chunk_retrieval_results": [],
  "debug": {
    "strategies": []
  }
}
```

真正 demo 里会有 17 条 triples、3 个 chunk retrieval results。

## 当前主路径是什么

当前主路径是：

```text
native-path1-rerank
```

它的意思是：

```text
Go 生成 path1 raw candidates
Python 给 path1 candidates 打分
Python 给 path2 做 vector rescore
Go 合并和输出
```

debug strategy：

```text
go_path1_rerank_path2_primitive_merge
```

这是判断有没有走对路径的重要标记。

## 各个 mode 是什么

这些 mode 是迁移过程中留下来的“验收开关”。它们不是乱加的，而是用来证明
每一步迁移没有把结果弄坏。

| Mode | 人话解释 | 是否当前主路径 |
| --- | --- | --- |
| `runtime-trace` | Python 把完整答案流程打包给 Go，Go 直接照着输出 | 否，回归基线 |
| `primitive-merge` | Python 给 path1/path2 两块结果，Go 合并 | 否，回归基线 |
| `rerank-merge` | Python 还给 path1 raw candidates，Go 发去 rerank 再合并 | 否，Phase 8 基线 |
| `native-path1-rerank` | Go 自己生成 path1 raw candidates，再让 Python 只打分 | 是 |

为什么保留旧 mode？

因为它们像考试答案。每次改新代码，都可以用旧 mode 对照，确认不是把已经
正确的东西改坏了。

## 当前哪些 Python 代码已经迁到 Go

已经迁到 Go 的包括：

1. graph/chunk loading。
2. `RetrieveRequest` / `RetrieveResult` contract。
3. CLI orchestration。
4. sidecar client。
5. chunk retrieval result 接入。
6. path1/path2 merge。
7. top_k sort/truncate。
8. debug strategy 和 meta。
9. demo golden gate。
10. path1 raw candidate generation。

特别是第 10 点，是 Phase 9 的核心结果。

以前 path1 raw candidates 是 Python 生成；现在 Go 生成，并且 @Gawain
验收时确认新路径没有调用 `/v1/retrieval/path1-triples`。

## 当前哪些还留在 Python

还留在 Python 的主要是 ML/vector 能力：

1. embedding model runtime。
2. FAISS index search。
3. path2 expansion/rescore。
4. triple rerank scoring。

这些不是普通业务逻辑，更像“模型基础设施”。

继续迁它们当然可以，但难度和收益不一样：

- 难度更高：要处理 tokenizer、ONNX、FAISS index、浮点误差、cache 格式。
- 学习架构收益更低：业务流程已经拆清楚了。
- 工程风险更高：一点点浮点差异就可能导致排序变化。

## 为什么现在可以先不继续全 Go

从“学习架构”的角度，核心收获已经完成：

1. 我们知道 Python 巨文件内部有哪些责任。
2. 我们把它拆成了清晰的 Go orchestration 和 Python sidecar。
3. 我们建立了稳定 contract。
4. 我们建立了 golden regression gate。
5. 我们已经让 Go 接管了最关键的流程控制和 path1 candidate generation。

继续往下迁，主要是在做“模型运行时迁移”，不是继续学习 retriever 架构。

除非目标变成：

- 生产环境不能部署 Python；
- 要减少 Python runtime 运维成本；
- 要把 embedding/FAISS 全部统一到 Go 服务里；
- 要做极致性能或单二进制交付；

否则现在的架构已经是一个合理停点。

## 如果未来还要继续全 Go，应该怎么走

如果未来真的要去掉 Python sidecar，可以按下面顺序做。

### Step 1：把 embedding runtime 迁到 Go

目标：

- Go 能把 question/triple text 转成向量。

可能方案：

- ONNX Runtime。
- 导出 sentence-transformers 模型。
- 固定 tokenizer 和 normalization。

风险：

- tokenizer 不一致。
- 向量归一化不一致。
- 浮点误差导致排序变化。

### Step 2：把 FAISS search 迁到 Go

目标：

- Go 能查 node/relation/chunk/triple 向量索引。

可能方案：

- Go 侧使用兼容向量索引库。
- 或导出索引为 Go 更容易读取的格式。

风险：

- FAISS index 格式不一定适合 Go 直接读。
- search 排序和 Python 不一致。
- cache 更新流程要重写。

### Step 3：把 rerank scoring 迁到 Go

目标：

- `/v1/retrieval/rerank-triples` 不再需要 Python。

需要：

- Go 生成 triple text。
- Go 跑 embedding。
- Go 算 cosine similarity。
- Go 加 relation bonus。
- Go 过滤 score <= 0.05。

风险：

- 只要 embedding 有一点差异，top_k 可能变化。

### Step 4：把 path2 expansion/rescore 迁到 Go

目标：

- `/v1/retrieval/path2-triples` 不再需要 Python。

需要复刻：

- triple index search。
- matched triple neighbor expansion。
- candidate dedupe。
- candidate rescore。
- threshold filter。

风险：

- path2 是当前剩余逻辑里最复杂的一块。
- 它依赖 graph、index、cache、score 多个细节。

### Step 5：删除 Python sidecar

只有当前面几步都通过 golden gate，才适合删 sidecar。

那时目标路径会变成：

```text
Question -> Go -> graph/chunk/vector index/model -> Go result
```

但现在还不是必要目标。

## 怎么判断以后有没有回退

当前最重要的验收命令：

```bash
GRAPH=/abs/path/youtu-graphrag/output/graphs/demo_new.json \
CHUNKS=/abs/path/youtu-graphrag/output/chunks/demo.txt \
GOLDEN=/abs/path/youtu-graphrag/output/retrieval_golden/demo.json \
SIDECAR_URL=http://127.0.0.1:8765 \
make demo-gates
```

它会跑这些 mode：

```text
runtime-trace
primitive-merge
rerank-merge
native-path1-rerank
```

每个 mode 都检查：

- `loader`
- `chunk`
- `triple`
- `full`

当前预期是全部 0 diff。

对 `native-path1-rerank` 还有一个硬检查：

```text
不能调用 /v1/retrieval/path1-triples
```

允许调用：

```text
/v1/embed
/v1/faiss/search
/v1/retrieval/rerank-triples
/v1/retrieval/path2-triples
```

## 文件入口索引

想看 Go 主流程：

```text
cmd/youtu-retriever/main.go
internal/retrieval/service.go
```

想看 sidecar client：

```text
internal/sidecar/client.go
```

想看 graph/chunk loader：

```text
internal/dataset/
internal/chunks/
```

想看 contracts：

```text
docs/contracts/sidecar_primitives.md
docs/contracts/path1_candidate_generation.md
docs/contracts/retrieve_result.schema.json
```

想看测试计划：

```text
docs/retriever_migration_test_plan.md
```

想跑 demo gates：

```text
scripts/run_demo_gates.sh
Makefile
```

## 当前结论

当前架构已经达到了“学习和工程化拆解”的目标：

- Python 巨文件不再是黑盒。
- Go 已经接管主要业务流程。
- Python 变成明确的 ML/vector sidecar。
- 每一步都有 golden gate 保护。
- 当前主路径已经不依赖 Python path1 primitive。

所以建议现在先停在这个架构点，做文档、评审和沉淀。

如果以后要继续迁，应该把目标改成“生产上去掉 Python runtime”，然后再
按 embedding、FAISS、rerank、path2 的顺序推进。不要为了“全 Go”而继续迁，
否则会把学习架构的问题变成模型基础设施迁移问题。
