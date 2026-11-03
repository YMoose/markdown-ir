// Package ir 实现 markdown-ir 的纯 IR 数据结构内核（规范 §2.1，Phase 1）：
// 固定节点外壳（Node Envelope）、基础核类型常量、方言（Dialect）注册表机制、
// 确定性 JSON 编解码与 LLVM Verifier 式合法性校验器。
// 本包不感知任何具体 Markdown 解析引擎（不变量 #4 引擎无关）。
package ir

import (
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// IRVersion 是当前 markdown-ir 的语义化版本号，即根信封键 "markdown-ir" 的唯一合法取值。
const IRVersion = "0.0.1"

// Node 是所有节点共享的固定物理外壳（MLIR Operation 式固定槽位，规范 §2.1.1）。
// 节点专有属性一律收敛进 Attrs 开放字典，顶层永不新增字段；配合反序列化的
// DisallowUnknownFields 即可严格拦截顶层拼写错误，同时无损保留未知属性（不变量 #3）。
type Node struct {
	Version  string         `json:"markdown-ir,omitempty"` // 仅根节点（type=="doc"）必填，格式魔数兼 SemVer
	ID       string         `json:"id,omitempty"`          // 块级节点稳定身份；行内节点禁止出现
	Type     string         `json:"type"`                  // 节点类型操作码
	Attrs    map[string]any `json:"attrs,omitempty"`       // 固有语法属性（Inherent），由 Verify 按 Type 校验
	Value    *string        `json:"value,omitempty"`       // 叶子纯文本载荷（text/inline_code/code_block/raw_*）
	Children []*Node        `json:"children,omitempty"`    // 容器节点有序子节点（Regions）
	Meta     map[string]any `json:"meta,omitempty"`        // 旁路元数据（Discardable），跨 Pass 强制透传
	Fallback []*Node        `json:"fallback,omitempty"`    // 扩展或高版本未知节点的降级渲染子树
	Pos      *SourcePos     `json:"pos,omitempty"`         // 可选源码区间，持久化可省略
	Origin   *SourceOrigin  `json:"origin,omitempty"`      // 可选源保真旁路数据，持久化可省略
	Errors   []Diagnostic   `json:"errors,omitempty"`      // 节点级解析/校验诊断
}

// SourcePos 是可选的源码字节区间（规范 §2.1.1）。
type SourcePos struct {
	Start int   `json:"start"`
	End   int   `json:"end"`
	Lines []int `json:"lines,omitempty"`
}

// SourceOrigin 是可选的源保真旁路数据（规范 §2.1.1）。
// Phase 2 引擎适配器将在此基础上扩展 entity_form 等字段。
type SourceOrigin struct {
	RawBytes string `json:"raw_bytes,omitempty"`
}

// Diagnostic 是一条节点级解析/校验诊断。
type Diagnostic struct {
	Kind    string `json:"kind"`            // 诊断类别（Diag* 常量）
	Message string `json:"message"`         // 人读消息
	Path    string `json:"path,omitempty"`  // 违例位置 JSON Pointer，如 /children/2/children/0
	Lines   []int  `json:"lines,omitempty"` // 关联源码行号
}

// 基础核块级节点类型常量（规范 §2.1.3 表 1，具体类型 9 类，x- 扩展另见 IsExtensionType）。
const (
	TypeDoc           = "doc"
	TypeHeading       = "heading"
	TypeParagraph     = "paragraph"
	TypeCodeBlock     = "code_block"
	TypeBlockquote    = "blockquote"
	TypeList          = "list"
	TypeListItem      = "list_item"
	TypeThematicBreak = "thematic_break"
	TypeRawBlock      = "raw_block"
)

// 基础核行内节点类型常量（规范 §2.1.3 表 2，具体类型 8 类，x- 扩展另见 IsExtensionType）。
const (
	TypeText       = "text"
	TypeStrong     = "strong"
	TypeEmphasis   = "emphasis"
	TypeInlineCode = "inline_code"
	TypeLink       = "link"
	TypeImage      = "image"
	TypeBreak      = "break"
	TypeRawInline  = "raw_inline"
)

var blockTypes = map[string]bool{
	TypeDoc:           true,
	TypeHeading:       true,
	TypeParagraph:     true,
	TypeCodeBlock:     true,
	TypeBlockquote:    true,
	TypeList:          true,
	TypeListItem:      true,
	TypeThematicBreak: true,
	TypeRawBlock:      true,
}

var inlineTypes = map[string]bool{
	TypeText:       true,
	TypeStrong:     true,
	TypeEmphasis:   true,
	TypeInlineCode: true,
	TypeLink:       true,
	TypeImage:      true,
	TypeBreak:      true,
	TypeRawInline:  true,
}

// extensionTypeRe 匹配方言扩展节点名 x-<dialect>/<op>（规范 §2.1.3）。
var extensionTypeRe = regexp.MustCompile(`^x-[a-z0-9-]+/[a-z0-9-]+$`)

// IsBlockType 报告 t 是否基础核块级节点；x- 扩展恒为 false（其块/行内归属由方言注册表声明）。
func IsBlockType(t string) bool { return blockTypes[t] }

// IsInlineType 报告 t 是否基础核行内节点；x- 扩展恒为 false（其块/行内归属由方言注册表声明）。
func IsInlineType(t string) bool { return inlineTypes[t] }

// IsExtensionType 报告 t 是否匹配方言扩展节点名 x-<dialect>/<op>。
func IsExtensionType(t string) bool { return extensionTypeRe.MatchString(t) }

// ContentKind 描述节点 value/children 的载荷形态（规范 §2.1.3 两张表的 value/children 列）。
type ContentKind int

const (
	// ContentEmpty 空叶：Value 必须为 nil 且无 Children（thematic_break/image/break）。
	ContentEmpty ContentKind = iota
	// ContentLeaf 文本叶：Value 必须非 nil 且无 Children（text/inline_code/code_block/raw_*）。
	ContentLeaf
	// ContentContainer 容器：Value 必须为 nil，Children 按 Children 策略与 MinChildren 校验。
	ContentContainer
)

// ChildPolicy 声明容器节点允许的子节点类别。
type ChildPolicy int

const (
	// ChildNone 禁止子节点。
	ChildNone ChildPolicy = iota
	// ChildBlocks 仅块级节点，禁止 doc 与 list_item（doc/blockquote/list_item 的 Content Model）。
	ChildBlocks
	// ChildListItems 仅 list_item（list 的 Content Model）。
	ChildListItems
	// ChildInlines 仅行内节点（heading/paragraph/strong/emphasis/link 的 Content Model）。
	ChildInlines
)

// String 返回策略的人读名称，用于诊断消息。
func (p ChildPolicy) String() string {
	switch p {
	case ChildNone:
		return "no children"
	case ChildBlocks:
		return "block nodes (excluding doc and list_item)"
	case ChildListItems:
		return "list_item nodes"
	case ChildInlines:
		return "inline nodes"
	}
	return "any nodes"
}

// AttrType 是 attrs 值的允许类型（表驱动 schema 用）。
type AttrType int

const (
	AttrString AttrType = iota
	AttrInt
	AttrBool
)

// AttrField 声明 attrs 中单个键的表驱动约束。
type AttrField struct {
	Required bool     // 必填
	Type     AttrType // 值类型
	MinInt   *int     // AttrInt 时的下界（含），nil 不限
	MaxInt   *int     // AttrInt 时的上界（含），nil 不限
	Enum     []string // AttrString 时的取值枚举，nil 不限
}

// AttrSchema 是一张 attrs 约束表；Closed 为 true 时拒绝一切未声明键（基础核节点均封闭）。
type AttrSchema struct {
	Fields map[string]AttrField
	Closed bool
}

// NodeSpec 声明一类节点的块/行内归属与结构约束（规范 §2.1.4 方言机制的最小载体）。
// 基础核与方言扩展共用同一表驱动机制：方言可通过 DialectRegistry 注册新 x- 节点，
// 或在基础核节点之上叠加扩展（如 GFM 为 list_item 增加 checked 属性）。
type NodeSpec struct {
	Type        string      // 节点类型操作码
	Block       bool        // true=块级节点（允许持有 id），false=行内节点（禁止 id）
	Kind        ContentKind // value/children 载荷形态
	Children    ChildPolicy // Kind==ContentContainer 时生效
	MinChildren int         // 容器最小子节点数（list/strong/emphasis 为 1，其余为 0）
	// ValidateAttrs 校验 attrs 字典，返回违例消息（空切片=通过）；nil 表示不校验。
	// 可由 AttrsValidator 从 AttrSchema 表编译获得，亦可提供任意闭包。
	ValidateAttrs func(attrs map[string]any) []string
}

// DialectRegistry 是挂载给 Verify/Normalize 的方言注册表（规范 §2.1.4.3）：
// 已注册节点按其 NodeSpec 严格校验；未注册的 x-<dialect>/<op> 节点仅校验
// 基本外壳与 fallback 合法性后原样放行（不变量 #5 方言可扩展、#3 无静默丢失）。
type DialectRegistry interface {
	Lookup(nodeType string) (NodeSpec, bool)
}

// AttrsValidator 将 AttrSchema 表编译为 NodeSpec.ValidateAttrs 校验函数；
// 输出消息按字典序排序以保证确定性。基础核与方言注册表复用同一机制。
func AttrsValidator(schema AttrSchema) func(map[string]any) []string {
	return func(attrs map[string]any) []string {
		var msgs []string
		for key, rule := range schema.Fields {
			val, present := attrs[key]
			if !present || val == nil {
				if rule.Required {
					msgs = append(msgs, "attrs."+key+": required but missing")
				}
				continue
			}
			if msg := rule.checkValue(key, val); msg != "" {
				msgs = append(msgs, msg)
			}
		}
		if schema.Closed {
			for key := range attrs {
				if _, declared := schema.Fields[key]; !declared {
					msgs = append(msgs, "attrs."+key+": unknown attribute")
				}
			}
		}
		sort.Strings(msgs)
		return msgs
	}
}

// checkValue 校验单个 attrs 值，返回违例消息（空串=通过）。
func (f AttrField) checkValue(key string, val any) string {
	switch f.Type {
	case AttrString:
		s, ok := val.(string)
		if !ok {
			return fmt.Sprintf("attrs.%s: must be a string (got %T)", key, val)
		}
		if len(f.Enum) > 0 && !containsStr(f.Enum, s) {
			return fmt.Sprintf("attrs.%s: must be one of [%s] (got %q)", key, strings.Join(f.Enum, ", "), s)
		}
	case AttrInt:
		i, ok := asInt64(val)
		if !ok {
			return fmt.Sprintf("attrs.%s: must be an integer (got %T)", key, val)
		}
		if (f.MinInt != nil && i < int64(*f.MinInt)) || (f.MaxInt != nil && i > int64(*f.MaxInt)) {
			return fmt.Sprintf("attrs.%s: must be an integer in range %s (got %d)", key, f.intRange(), i)
		}
	case AttrBool:
		if _, ok := val.(bool); !ok {
			return fmt.Sprintf("attrs.%s: must be a boolean (got %T)", key, val)
		}
	}
	return ""
}

// intRange 返回形如 "[1, 6]" 的区间描述；无界侧显示 -inf/+inf。
func (f AttrField) intRange() string {
	lo, hi := "-inf", "+inf"
	if f.MinInt != nil {
		lo = strconv.FormatInt(int64(*f.MinInt), 10)
	}
	if f.MaxInt != nil {
		hi = strconv.FormatInt(int64(*f.MaxInt), 10)
	}
	return "[" + lo + ", " + hi + "]"
}

func containsStr(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

// asInt64 将值解释为整数；接受 int/int64 及可解析为整数的 json.Number
// （UnmarshalIR 数值规范化后整数统一为 int64，float64 一律视为非整数以保持严格）。
func asInt64(v any) (int64, bool) {
	switch t := v.(type) {
	case int:
		return int64(t), true
	case int64:
		return t, true
	case json.Number:
		if i, err := t.Int64(); err == nil {
			return i, true
		}
	}
	return 0, false
}

// stringPtr 返回字符串指针（Value 字段为 *string 以区分缺失与空串）。
func stringPtr(s string) *string { return &s }

// ---------------------------------------------------------------------------
// 便捷构造器（Builders）：数值型 attrs 一律直接落 int64，
// 与 UnmarshalIR 的数值规范化对齐，保证 Marshal→Unmarshal 往返 DeepEqual 稳定。
// ---------------------------------------------------------------------------

// NewDoc 构造文档根节点，自动携带 "markdown-ir": "0.0.1" 版本信封。
func NewDoc(id string, children ...*Node) *Node {
	return &Node{Version: IRVersion, ID: id, Type: TypeDoc, Children: children}
}

// NewHeading 构造标题节点，depth 必须为 1..6 的整数。
func NewHeading(id string, depth int64, children ...*Node) *Node {
	return &Node{
		ID:       id,
		Type:     TypeHeading,
		Attrs:    map[string]any{"depth": depth},
		Children: children,
	}
}

// NewParagraph 构造段落节点。
func NewParagraph(id string, children ...*Node) *Node {
	return &Node{ID: id, Type: TypeParagraph, Children: children}
}

// NewText 构造纯文本行内节点。
func NewText(value string) *Node {
	return &Node{Type: TypeText, Value: stringPtr(value)}
}

// NewStrong 构造强强调（加粗）节点。
func NewStrong(children ...*Node) *Node {
	return &Node{Type: TypeStrong, Children: children}
}

// NewEmphasis 构造强调（斜体）节点。
func NewEmphasis(children ...*Node) *Node {
	return &Node{Type: TypeEmphasis, Children: children}
}

// NewInlineCode 构造行内代码节点。
func NewInlineCode(value string) *Node {
	return &Node{Type: TypeInlineCode, Value: stringPtr(value)}
}

// NewCodeBlock 构造代码块节点；lang 为空串时不写 attrs（规范默认空串省略）。
func NewCodeBlock(id, lang, value string) *Node {
	n := &Node{ID: id, Type: TypeCodeBlock, Value: stringPtr(value)}
	if lang != "" {
		n.Attrs = map[string]any{"lang": lang}
	}
	return n
}

// NewLink 构造超链接节点；title 为空串时省略（规范默认省略）。
func NewLink(url, title string, children ...*Node) *Node {
	attrs := map[string]any{"url": url}
	if title != "" {
		attrs["title"] = title
	}
	return &Node{Type: TypeLink, Attrs: attrs, Children: children}
}

// NewImage 构造图片节点（alt 拍平为纯文本属性，允许空串）。
func NewImage(url, alt, title string) *Node {
	attrs := map[string]any{"url": url, "alt": alt}
	if title != "" {
		attrs["title"] = title
	}
	return &Node{Type: TypeImage, Attrs: attrs}
}

// NewList 构造列表容器节点（默认属性 ordered:false/start:1/tight:true 均省略）。
func NewList(id string, items ...*Node) *Node {
	return &Node{ID: id, Type: TypeList, Children: items}
}

// NewListItem 构造列表项节点（独立块级容器，父节点只能为 list）。
func NewListItem(id string, children ...*Node) *Node {
	return &Node{ID: id, Type: TypeListItem, Children: children}
}

// NewBlockquote 构造引用容器块。
func NewBlockquote(id string, children ...*Node) *Node {
	return &Node{ID: id, Type: TypeBlockquote, Children: children}
}

// NewThematicBreak 构造分割线空叶节点。
func NewThematicBreak(id string) *Node {
	return &Node{ID: id, Type: TypeThematicBreak}
}

// NewRawBlock 构造块级无损逃生舱节点（format 见规范枚举）。
func NewRawBlock(id, format, value string) *Node {
	return &Node{
		ID:    id,
		Type:  TypeRawBlock,
		Attrs: map[string]any{"format": format},
		Value: stringPtr(value),
	}
}

// NewRawInline 构造行内无损逃生舱节点（行内节点禁止 id）。
func NewRawInline(format, value string) *Node {
	return &Node{
		Type:  TypeRawInline,
		Attrs: map[string]any{"format": format},
		Value: stringPtr(value),
	}
}
