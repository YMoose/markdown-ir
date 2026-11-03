# markdown-ir

**面向双向无损同步与结构化 Diff/Patch 的 Markdown 语义中间表示（Semantic IR）规范与 Go 核心库。**

[![Go Version](https://img.shields.io/badge/Go-1.26+-00ADD8?logo=go)](./go.mod)
[![IR Spec](https://img.shields.io/badge/Spec-0.0.1-blue)](./markdown-ir-spec.md)
[![Dependencies](https://img.shields.io/badge/Dependencies-Zero-brightgreen)](./go.mod)

---

## 为什么需要 `markdown-ir`？

传统的 `git diff` 工作在纯文本行级（Line-based）层面，缺乏文档结构语义：

- **排版噪音与语义变更混杂**：列表符号从 `*` 改为 `-`、ATX 与 Setext 标题切换、段落折行重排，都会在文本 Diff 中产生大量伪变更；
- **结构化编辑无法直接表达**：“移动一个列表项”“将二级标题整体降为三级”“替换某个 Callout 块”在行级补丁中只能退化为脆弱的删行与插行；
- **现有解析器 AST 与引擎强耦合**：诸如 Lute（将 `#`、`**` 等标记符作为 AST 子节点）或 goldmark（节点绑定源码字节切片偏移）的内部语法树面向单一渲染或编辑器场景设计，不适合作为跨引擎、可持久化的交换格式。

`markdown-ir` 借鉴 **LLVM IR / MLIR** 的编译器中间表示思想，定义了一套**与具体解析引擎解耦、以规范化 JSON 承载的 Markdown 语义 IR**，将文档同步拆分为对偶的两层链路：

```text
  文本层 (Text Layer)            Markdown  <──────────>  git diff
                                   │  ▲                    │
                            Parse  │  │  Emit              │ 语义提纯
                                   ▼  │                    ▼
  结构语义层 (Semantic Layer)    IR JSON   <──────────>  JSON Patch
                                         DiffIR / ApplyPatch
```

---

## 核心设计原则

1. **五条核心不变量（Invariants）**
   - **幂等性（Idempotence）**：`emit(parse(emit(x))) == emit(x)`，`normalize(parse(emit(x))) == normalize(x)`。
   - **身份保持（Identity Preservation）**：跨轮次编辑或移动块级节点时，其 `id` 保持稳定；新增块分配新 `id`。
   - **无静默丢失（No Silent Loss）**：超出核心范围的语法或未知高版本节点必须通过逃生舱节点（`raw_block` / `raw_inline`）或属性字典原样保留，绝不静默丢弃。
   - **引擎无关性（Engine Agnosticism）**：IR 数据结构不包含任何特定解析器私有概念。
   - **方言可扩展（Dialect Extensibility）**：以 CommonMark 0.31.2 为基础核心，扩展语法通过注册方言（Dialect）接入，节点物理外壳永不改变。

2. **MLIR 风格的固定 11 字段节点外壳（Node Envelope）**
   汲取 LLVM IR 早期属性位掩码膨胀的历史教训，`markdown-ir` 冻结顶层 JSON 键集合（`markdown-ir`、`id`、`type`、`attrs`、`value`、`children`、`meta`、`fallback`、`pos`、`origin`、`errors`），节点特有属性统一收敛至受 Schema 约束的 `attrs` 字典，并在反序列化时开启 `DisallowUnknownFields()` 彻底杜绝拼写错误导致的静默丢字段。

3. **Inherent Attributes (`attrs`) 与 Discardable Metadata (`meta`) 分离**
   - `attrs`：承载影响文档语义与渲染结果的固有属性，由 `Verify` 按节点闭合 Schema 严格校验；
   - `meta`：承载业务旁路元数据（如评论锚点、协同光标、审查状态），对核心 Pass 透明透传，同时禁止占用外壳保留字及 `_` / `sys:` 前缀。

4. **精确的块级身份边界（Block-Only Identity）**
   所有且仅块级节点（含 `doc` 与 `list_item`）可携带全局唯一 `id`；行内节点严禁携带 `id`。这使 `list_item` 重排、段落移动等操作可被基于 `id` 的 JSON Patch 直接寻址，同时避免行内碎片化打标。

---

## 标准节点指令集（`0.0.1` Base Core）

| 类别 | 节点 `type` | 内容模型 (`ContentKind` / 子节点约束) | 属性 Schema (`attrs`) |
| :--- | :--- | :--- | :--- |
| **Root** | `doc` | `Container` (`Block[]`，不含 `doc`/`list_item`) | 无 |
| **Block** | `heading` | `Container` (`Inline[]`) | `depth`: `int` ∈ `[1, 6]`（必填，统一 ATX/Setext） |
| **Block** | `paragraph` | `Container` (`Inline[]`) | 无 |
| **Block** | `code_block` | `Leaf` (`value` 必填，统一缩进与围栏代码块) | `lang?`: `string`, `info?`: `string` |
| **Block** | `blockquote` | `Container` (`Block[]`) | 无 |
| **Block** | `list` | `Container` (`list_item[]`，至少 1 项) | `ordered?`: `bool`, `start?`: `int`, `tight?`: `bool` |
| **Block** | `list_item` | `Container` (`Block[]`，父节点必须为 `list`) | 基础核心无（GFM 方言可扩展 `checked?: bool`） |
| **Block** | `thematic_break` | `Empty` | 无 |
| **Block** | `raw_block` | `Leaf` (`value` 必填) | `format`: `"markdown" \| "html" \| "ir-unknown" \| "invalid-x-node"` |
| **Inline** | `text` | `Leaf` (`value` 必填) | 无 |
| **Inline** | `strong` | `Container` (`Inline[]`，至少 1 项) | 无 |
| **Inline** | `emphasis` | `Container` (`Inline[]`，至少 1 项) | 无 |
| **Inline** | `inline_code` | `Leaf` (`value` 必填) | 无 |
| **Inline** | `link` | `Container` (`Inline[]`，后代禁止嵌套 `link`) | `url`: `string`（必填）, `title?`: `string` |
| **Inline** | `image` | `Empty`（`alt` 拍平为纯文本属性，无子节点） | `url`: `string`（必填）, `alt`: `string`（必填）, `title?`: `string` |
| **Inline** | `break` | `Empty` | `hard?`: `bool` |
| **Inline** | `raw_inline` | `Leaf` (`value` 必填) | `format`: `"markdown" \| "html" \| "ir-unknown"` |
| **Dialect** | `x-<dialect>/<op>` | 由 `DialectRegistry` 定义，支持同级 `fallback` 降级子树 | 由方言 Schema 定义（如 `x-wiki/link`、`x-callout/block`） |

---

## IR JSON 示例

```json
{
  "markdown-ir": "0.0.1",
  "id": "20261029-doc01",
  "type": "doc",
  "children": [
    {
      "id": "20261029-aaaa",
      "type": "heading",
      "attrs": { "depth": 1 },
      "children": [
        { "type": "text", "value": "搜索服务重构评审" }
      ]
    },
    {
      "id": "20261029-bbbb",
      "type": "paragraph",
      "children": [
        { "type": "text", "value": "目标是将核心接口延迟降低 " },
        { "type": "strong", "children": [{ "type": "text", "value": "30%" }] },
        { "type": "text", "value": "，详见 " },
        {
          "type": "x-wiki/link",
          "attrs": { "target": "延迟优化方案", "alias": "优化方案" },
          "fallback": [{ "type": "text", "value": "[[延迟优化方案|优化方案]]" }]
        },
        { "type": "text", "value": "。" }
      ]
    },
    {
      "id": "20261029-dddd",
      "type": "x-callout/block",
      "attrs": { "kind": "note", "title": "注意" },
      "children": [
        {
          "id": "20261029-p002",
          "type": "paragraph",
          "children": [{ "type": "text", "value": "请先完成阶段 1 校验。" }]
        }
      ],
      "fallback": [
        {
          "id": "20261029-fb01",
          "type": "blockquote",
          "children": [
            {
              "id": "20261029-fb02",
              "type": "paragraph",
              "children": [{ "type": "text", "value": "注意：请先完成阶段 1 校验。" }]
            }
          ]
        }
      ]
    }
  ]
}
```

---

## 快速开始（Go 核心库 `ir`）

本仓库零第三方依赖，直接构建、校验与序列化 IR 语法树：

```go
package main

import (
	"fmt"
	"log"

	"markdown-ir/ir"
)

func main() {
	// 1. 使用构造器组装 IR 树
	doc := ir.NewDoc("doc-01",
		ir.NewHeading("h-01", 1, ir.NewText("Hello markdown-ir")),
		ir.NewParagraph("p-01",
			ir.NewText("支持 "),
			ir.NewStrong(ir.NewText("强类型校验")),
			ir.NewText(" 与确定性 JSON 序列化。"),
		),
	)

	// 2. 运行 LLVM-Verifier 风格聚合校验
	if err := ir.Verify(doc, ir.VerifyOptions{RequireBlockID: true}); err != nil {
		log.Fatalf("verify failed: %v", err)
	}

	// 3. 确定性 Canonical JSON 序列化（不转义 <>&，字段与字典键顺序稳定）
	data, err := ir.MarshalIRIndent(doc)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println(string(data))

	// 4. 严格反序列化（拒绝未知字段，自动将整数与浮点数规范化为 int64 / float64）
	roundTrip, err := ir.UnmarshalIR(data)
	if err != nil {
		log.Fatal(err)
	}
	_ = roundTrip
}
```

运行测试：

```bash
go test -v ./ir
```

---

## 代码结构与当前实现状态

```text
.
├── markdown-ir-spec.md   # Markdown 语义 IR 完整规范（v0.0.1）
├── go.mod                # Go 模块定义（零外部依赖）
└── ir/
    ├── types.go          # 节点外壳、类型常量、AttrSchema、DialectRegistry 与节点构造器
    ├── verify.go         # Verify 聚合校验器（8 类 DiagnosticKind + JSON Pointer 路径定位）
    ├── json.go           # UnmarshalIR 严格解码 + 数字类型归一化、MarshalIR 确定性编码
    └── ir_test.go        # 规范样例、往返一致性、严格性、29 项违规矩阵与方言注册测试
```

### 路线图（Roadmap）

- [x] **阶段 1a：纯 IR 内核与校验器（`v0.0.1` 已实现）**
  - [x] 固定 11 字段节点外壳与 CommonMark 核心节点构造器（`ir/types.go`）
  - [x] 严格 JSON 编解码与数字类型归一化（`ir/json.go`）
  - [x] 全量结构/属性/ID 边界/元数据聚合校验器 `Verify` 与 `DialectRegistry` 扩展接口（`ir/verify.go`）
- [ ] **阶段 1b：规范化与版本演进（规范已锁定，待实现）**
  - [ ] `Normalize(doc)` 三遍收敛规范化 Pass（换行符统一、相邻 `text` 合并、默认属性剥离）
  - [ ] `LoadIR` / `AutoUpgrade` 跨版本迁移链与未知高版本节点向后兼容包装
- [ ] **阶段 2：Markdown 引擎适配层（`Parse` / `Emit`）**
  - [ ] 基于候选解析引擎的双向转换与块级 `id` 三级确定性对齐算法
- [ ] **阶段 3：结构化增量算子（`DiffIR` / `ApplyPatch`）**
  - [ ] 块级 `id` 寻址的树 Diff 与 JSON Patch 生成/应用

详细设计推导、节点合法性矩阵与方言规范请参阅 [`markdown-ir-spec.md`](./markdown-ir-spec.md)。
