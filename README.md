# HelpDesk Agent — 智能客服系统

基于 Go + [Eino](https://github.com/cloudwego/eino) 框架的超级智能体客服系统，使用 Supervisor 编排模式，支持多模型、流式响应、三层记忆。

## 架构

```
┌─ ADK Runner ──────────────────────────────────────────┐
│                                                        │
│  Supervisor (help_desk_supervisor)                     │
│    │  LLM 决定调用工具或 transfer 到 SubAgent          │
│    │                                                   │
│    ├── Tools:                                          │
│    │   ├── intent_classify     意图分类                │
│    │   └── compliance_check    合规检查（PII + 规则）  │
│    │                                                   │
│    ├── SubAgents:                                      │
│    │   ├── knowledge_rag       RAG 检索 + LLM 合成     │
│    │   └── ticket_handler      工单创建/查询/更新      │
│    │                                                   │
│  三层记忆: working → short-term → long-term            │
└───────────────────────────────────────────────────────┘
```

## 环境变量

| 变量 | 说明 | 默认值 |
|------|------|--------|
| `LLM_DEEPSEEK_API_KEY` | DeepSeek API 密钥 | **必填** |
| `LLM_DEEPSEEK_BASE_URL` | DeepSeek 端点 | `https://api.openai.com/v1` |
| `LLM_DEEPSEEK_MODEL` | DeepSeek 模型名 | `deepseek` |
| `LLM_GEMINI_API_KEY` | Gemini API 密钥 | **必填** |
| `LLM_GEMINI_BASE_URL` | Gemini 端点 | `https://api.openai.com/v1` |
| `LLM_GEMINI_MODEL` | Gemini 模型名 | `gemini` |
| `MEMORY_DATA_DIR` | 长期记忆存储目录 | `./data/memory` |
| `PORT` | HTTP 端口 | `8080` |

> 支持 OpenAI 兼容接口，Gemini/DeepSeek 配置对应的 base_url 即可。

## 快速开始

```bash
export LLM_DEEPSEEK_API_KEY=your_key
export LLM_GEMINI_API_KEY=your_key
go run main.go
```

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

响应以 SSE 流式返回：

```
data: 我来查一下您的订单状态...
data: 请稍等...
event: done
data: [DONE]
```

### 健康检查

```
GET /health
→ {"status": "ok"}
```

## 项目结构

```
├── main.go                   入口
├── agent/
│   ├── help_desk.go          Supervisor 编排
│   └── helpdesk/
│       ├── state.go          状态契约
│       ├── intent_classify.go 意图分类
│       ├── compliance_check.go 合规检查
│       ├── knowledge_rag.go   RAG 检索
│       └── ticket_handler.go  工单处理
├── llm/
│   └── registry.go           LLM 客户端注册
├── memory/
│   ├── working_memory.go      工作记忆（进程内）
│   ├── short_term.go          短期记忆（轮次管理）
│   └── long_term.go           长期记忆（文件持久化）
├── api/
│   └── server.go              Gin + SSE 端点
└── tracing/
    └── tracer.go              调用链追踪
```

## 数据流

1. 用户消息 → `POST /api/chat`
2. Supervisor 调 `intent_classify` 判断意图
3. 根据意图选择：直接回复 / transfer 到 `knowledge_rag` / transfer 到 `ticket_handler`
4. 可选调 `compliance_check` 扫描 PII 和合规规则
5. SSE 流式推送回复
6. 短期记忆 + 长期记忆异步写入

## 构建

```bash
go build -o helpdesk-agent .
```
