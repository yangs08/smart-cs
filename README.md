# HelpDesk Agent — 智能客服系统

基于 Go + [Eino](https://github.com/cloudwego/eino) ADK 的智能客服 Agent，使用 Supervisor 编排 + 多模型路由 + 混合检索 RAG + SSE 流式对话。

## 架构

```
┌─ ADK Runner ────────────────────────────────────────────────┐
│                                                               │
│  Supervisor (help_desk_supervisor)                             │
│    │  LLM 决定调用工具（直接函数或 SubAgent）                  │
│    │                                                           │
│    ├── Tools:                                                  │
│    │   ├── intent_classify     意图分类（fast 模型）           │
│    │   └── compliance_check    合规检查（fast 模型）           │
│    ├── SubAgents (wrapped as tools via NewAgentTool):          │
│    │   ├── knowledge_rag       RAG 检索 + 合成（reasoning）    │
│    │   └── ticket_handler      工单创建/查询/更新              │
│    │                                                           │
│   三层记忆: working → short-term → long-term                   │
│   向量 + BM25 混合检索 → Qdrant                                │
└─────────────────────────────────────────────────────────────┘
```

## 模型路由

按任务角色分配不同规模模型，不配置时静默降级到默认模型：

| 角色 | 环境变量 | 默认值 | 用途 |
|------|----------|--------|------|
| `default` | `LLM_CHAT_MODEL` | `qwen2.5:7b` | Supervisor 编排、工单处理 |
| `fast` | `LLM_FAST_MODEL` | 同 default | 意图分类、合规检查 |
| `reasoning` | `LLM_REASONING_MODEL` | 同 default | RAG 答案合成 |

所有模型共用 `OLLAMA_BASE_URL`（默认 `http://localhost:11434`）和 `LLM_API_KEY`（默认 `ollama`）。

## 环境变量

| 变量 | 说明 | 默认值 |
|------|------|--------|
| `LLM_CHAT_MODEL` | 默认对话模型 | `qwen2.5:7b` |
| `LLM_FAST_MODEL` | 快速任务模型（可选） | 同 `LLM_CHAT_MODEL` |
| `LLM_REASONING_MODEL` | 推理模型（可选） | 同 `LLM_CHAT_MODEL` |
| `OLLAMA_BASE_URL` | Ollama 服务地址 | `http://localhost:11434` |
| `LLM_API_KEY` | API 密钥 | `ollama` |
| `QDRANT_URL` | Qdrant 地址 | `http://localhost:6334` |
| `QDRANT_COLLECTION` | Qdrant 集合名 | `helpdesk_knowledge` |
| `PORT` | HTTP 端口 | `8080` |
| `MEMORY_DATA_DIR` | 长期记忆目录 | `./data/memory` |

## 前置依赖

- [Ollama](https://ollama.com) — 本地 LLM 推理
- [Qdrant](https://qdrant.tech) — 向量数据库（可选，无 Qdrant 时使用纯 BM25 检索）

```bash
# 拉取默认模型
ollama pull qwen2.5:7b

# 可选：配置不同角色模型
ollama pull qwen2.5:3b   # fast 模型
ollama pull qwen2.5:14b  # reasoning 模型

# 启动 Qdrant（可选）
docker run -d -p 6333:6333 -p 6334:6334 qdrant/qdrant
```

## 快速开始

```bash
# 最小配置（所有角色使用同一模型）
export LLM_CHAT_MODEL=qwen2.5:7b
go run main.go
```

```bash
# 多模型配置
export LLM_CHAT_MODEL=qwen2.5:7b
export LLM_FAST_MODEL=qwen2.5:3b
export LLM_REASONING_MODEL=qwen2.5:14b
go run main.go
```

打开 `http://localhost:8080` 进入聊天界面。

## API

### 聊天

```
POST /api/chat
Content-Type: application/json

{
  "session_id": "sess_001",
  "message": "我的订单还没到"
}
```

SSE 流式响应：

```
data: 我来查一下您的订单状态...
data: 已为您查询到相关信息...
event: done
data: [DONE]
```

### 健康检查

```
GET /health
→ {"status":"ok","qdrant":"ok","ollama":"ok"}
```

### 指标

```
GET /metrics
→ Prometheus 格式指标
```

## 项目结构

```
├── main.go                      入口 + 优雅关闭
├── agent/
│   ├── help_desk.go             Supervisor 编排（NewAgentTool 模式）
│   └── helpdesk/
│       ├── state.go             状态契约
│       ├── intent_classify.go   意图分类
│       ├── compliance_check.go  合规检查（PII + 规则）
│       ├── knowledge_rag.go     RAG 检索 + LLM 合成
│       └── ticket_handler.go    工单处理
├── llm/
│   └── router.go                多模型路由（按角色选择）
├── memory/
│   ├── working_memory.go        工作记忆（进程内 Map）
│   ├── short_term.go            短期记忆（轮次管理）
│   └── long_term.go             长期记忆（JSONL 文件持久化）
├── knowledge/
│   ├── store.go                 BM25 + 向量混合检索 + RRF 融合
│   └── embedder.go              Ollama 嵌入服务
├── api/
│   ├── server.go                Gin + SSE 端点
│   ├── metrics.go               Prometheus 指标
│   └── middleware.go            请求日志中间件
├── tracing/
│   └── tracer.go                调用链追踪（Eino Callback）
└── frontend/
    └── chat.html                聊天 UI（SSE 流式展示）
```

## 数据流

1. 用户消息 → `POST /api/chat` → SSE 流式返回
2. Server 查询长期记忆 → 调 `runner.Query` 启动 Agent 执行
3. Supervisor 调 `intent_classify` 判断意图 → 按需调用子工具
4. `knowledge_rag` 执行混合检索（BM25 + 向量 + RRF）→ LLM 合成答案
5. `ticket_handler` 创建/查询/更新工单
6. 可选调 `compliance_check` 扫描 PII 和合规规则
7. 长期记忆异步写入（JSONL 持久化）

## 构建

```bash
go build -o helpdesk-agent .
```
