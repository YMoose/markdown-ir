package ir

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// 诊断类别常量。
const (
	DiagRootEnvelope = "root_envelope" // 根信封（type=="doc" 且 "markdown-ir"=="0.0.1"）
	DiagUnknownType  = "unknown_type"  // 既非基础核、非 x- 扩展、也未在注册表登记的类型
	DiagIDBoundary   = "id_boundary"   // 行内禁 id / RequireBlockID 下块级必填
	DiagDuplicateID  = "duplicate_id"  // 全树非空 id 冲突
	DiagContentModel = "content_model" // value/children 互斥与 Content Model 违例
	DiagAttrSchema   = "attr_schema"   // attrs 必填缺失 / 类型不符 / 未知键 / 枚举越界
	DiagMetaKey      = "meta_key"      // meta 使用系统保留键或 _、sys: 前缀
	DiagMetaValue    = "meta_value"    // meta 值类型超出 string|number|boolean|string[]
)

// VerifyOptions 配置 Verify 行为。
type VerifyOptions struct {
	// Registry 为可选方言注册表：已注册类型（含对基础核类型的扩展，如 GFM
	// 为 list_item 叠加 checked）按其 NodeSpec 校验；nil 时仅按基础核表校验。
	Registry DialectRegistry
	// RequireBlockID 为 true 时（对应 embedded 锚点模式）所有块级节点 id 必填。
	RequireBlockID bool
}

// VerifyError 聚合一次 Verify 发现的全部违例（LLVM Verifier 式，非遇错即停）。
type VerifyError struct {
	Diagnostics []Diagnostic
}

// Error 汇总全部违例，每条形如 "[kind] path: message"，逐行缩进呈现。
func (e *VerifyError) Error() string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "markdown-ir: verify found %d violation(s)", len(e.Diagnostics))
	for _, d := range e.Diagnostics {
		sb.WriteString("\n\t[")
		sb.WriteString(d.Kind)
		sb.WriteString("] ")
		if d.Path != "" {
			sb.WriteString(d.Path)
			sb.WriteString(": ")
		}
		sb.WriteString(d.Message)
	}
	return sb.String()
}

// Verify 校验整棵 IR 树（PLAN Step 1.2 六类校验），通过返回 nil；
// 否则返回聚合全部违例的 *VerifyError。
func Verify(doc *Node, opts VerifyOptions) error {
	if doc == nil {
		return &VerifyError{Diagnostics: []Diagnostic{{
			Kind:    DiagRootEnvelope,
			Message: "root node is nil",
		}}}
	}
	v := &verifier{opts: opts, ids: make(map[string]string)}
	v.walk(doc, "", true, "", false)
	if len(v.diags) == 0 {
		return nil
	}
	return &VerifyError{Diagnostics: v.diags}
}

// verifier 是单次校验的游走器：收集全部违例并登记 id 首现路径。
type verifier struct {
	opts  VerifyOptions
	diags []Diagnostic
	ids   map[string]string // 非空 id -> 首次出现路径
}

// nodeClass 是类型解析后的块/行内归属。
type nodeClass int

const (
	classUnknown  nodeClass = iota // 未注册且非 x- 的非法类型（已报 unknown_type）
	classBlock                     // 块级（按 spec.Block 判定，含注册的块级 x- 节点）
	classInline                    // 行内（含注册的行内 x- 节点）
	classWildcard                  // 未注册 x-：块/行内归属未知，按通配放行（规范 §2.1.4.3）
)

func (v *verifier) report(kind, path, format string, args ...any) {
	v.diags = append(v.diags, Diagnostic{
		Kind:    kind,
		Path:    path,
		Message: fmt.Sprintf(format, args...),
	})
}

// walk 游走单个节点：path 为该节点的 JSON Pointer（根为 ""），
// parentType 用于 list_item 父约束，inLink 标记是否处于 link 后代中。
func (v *verifier) walk(n *Node, path string, isRoot bool, parentType string, inLink bool) {
	if n == nil {
		v.report(DiagContentModel, path, "tree contains a nil node")
		return
	}

	// ① 根信封：根必须 type=="doc" 且携带 "markdown-ir": "0.0.1"；非根禁止版本头。
	if isRoot {
		if n.Type != TypeDoc {
			v.report(DiagRootEnvelope, path, `root node must have type "doc" (got %q)`, n.Type)
		}
		if n.Version != IRVersion {
			v.report(DiagRootEnvelope, path, `root node must carry "markdown-ir": %q (got %q)`, IRVersion, n.Version)
		}
	} else if n.Version != "" {
		v.report(DiagRootEnvelope, path, `"markdown-ir" version header is only allowed on the root doc node`)
	}

	spec, class := v.lookupSpec(n.Type)

	// ② id 归属边界（规范 §2.1.2）与全树非空 id 唯一性。
	switch class {
	case classInline:
		if n.ID != "" {
			v.report(DiagIDBoundary, path, "inline node %q must not carry id %q", n.Type, n.ID)
		}
	case classBlock:
		if v.opts.RequireBlockID && n.ID == "" {
			v.report(DiagIDBoundary, path, "block node %q requires a non-empty id when RequireBlockID is set", n.Type)
		}
	}
	if n.ID != "" {
		if first, dup := v.ids[n.ID]; dup {
			v.report(DiagDuplicateID, path, "duplicate id %q (first seen at %q)", n.ID, first)
		} else {
			v.ids[n.ID] = path
		}
	}

	switch class {
	case classUnknown:
		v.report(DiagUnknownType, path,
			"unknown node type %q (not in base core, not an x- extension, not in dialect registry)", n.Type)
	case classWildcard:
		// 未注册 x- 扩展（规范 §2.1.4.3）：attrs/value/children 原样放行，
		// 不做 Content Model 与 attrs Schema 约束，仅递归校验子树与 fallback 自身合法性。
	default:
		v.checkContentModel(n, path, spec)
		if spec.ValidateAttrs != nil {
			for _, msg := range spec.ValidateAttrs(n.Attrs) {
				v.report(DiagAttrSchema, path, "%s %s", n.Type, msg)
			}
		}
		// 已注册类别的 fallback 子树须为同类节点（规范 §2.1.3：块级带 Block[]、行内带 Inline[]）。
		fbPolicy := ChildInlines
		if class == classBlock {
			fbPolicy = ChildBlocks
		}
		for i, f := range n.Fallback {
			if !v.childAllowed(fbPolicy, f) {
				v.report(DiagContentModel, fallbackPath(path, i),
					"fallback of %s must contain %s (got %s)", n.Type, fbPolicy, typeOf(f))
			}
		}
	}

	// 特殊结构约束：link 任意后代禁止再嵌 link；list_item 直接父节点只能是 list。
	if inLink && n.Type == TypeLink {
		v.report(DiagContentModel, path, "link must not be nested inside another link")
	}
	if n.Type == TypeListItem && parentType != TypeList {
		v.report(DiagContentModel, path, "list_item is only allowed as a direct child of list (parent %q)", parentType)
	}

	// ⑤ meta 系统保留键与值类型。
	v.checkMeta(n, path)

	// ⑥ 递归 children 与 fallback。fallback 是降级渲染子树，独立于 link 嵌套上下文。
	childInLink := inLink || n.Type == TypeLink
	for i, c := range n.Children {
		v.walk(c, childPath(path, i), false, n.Type, childInLink)
	}
	for i, f := range n.Fallback {
		v.walk(f, fallbackPath(path, i), false, n.Type, false)
	}
}

// lookupSpec 按「方言注册表 → 基础核表 → 未注册 x- 通配」顺序解析节点规格
// （注册表优先，允许方言扩展/覆盖基础核节点，如 GFM 的 list_item.attrs.checked）。
func (v *verifier) lookupSpec(t string) (NodeSpec, nodeClass) {
	if v.opts.Registry != nil {
		if spec, ok := v.opts.Registry.Lookup(t); ok {
			return spec, classOfSpec(spec)
		}
	}
	if spec, ok := BaseSpec(t); ok {
		return spec, classOfSpec(spec)
	}
	if IsExtensionType(t) {
		return NodeSpec{Type: t}, classWildcard
	}
	return NodeSpec{Type: t}, classUnknown
}

func classOfSpec(spec NodeSpec) nodeClass {
	if spec.Block {
		return classBlock
	}
	return classInline
}

// childAllowed 判断策略下是否允许该子节点；未注册 x- 视为通配符，
// 在块容器与行内容器中均放行（其真实归属只能由方言注册表判定）。
func (v *verifier) childAllowed(policy ChildPolicy, child *Node) bool {
	if child == nil || policy == ChildNone {
		return false
	}
	_, class := v.lookupSpec(child.Type)
	switch policy {
	case ChildBlocks:
		if class == classWildcard {
			return true
		}
		return class == classBlock && child.Type != TypeDoc && child.Type != TypeListItem
	case ChildListItems:
		return child.Type == TypeListItem
	case ChildInlines:
		return class == classInline || class == classWildcard
	}
	return true
}

// checkContentModel 按 §2.1.3 两张表校验 value/children 互斥与 Content Model；
// 基础核节点禁带 fallback（fallback 保留给扩展与高版本未知节点）。
func (v *verifier) checkContentModel(n *Node, path string, spec NodeSpec) {
	if len(n.Fallback) != 0 {
		v.report(DiagContentModel, path, "%s must not carry fallback (reserved for extension nodes)", n.Type)
	}
	switch spec.Kind {
	case ContentEmpty:
		if n.Value != nil {
			v.report(DiagContentModel, path, "%s must not carry value", n.Type)
		}
		if len(n.Children) != 0 {
			v.report(DiagContentModel, path, "%s must not carry children", n.Type)
		}
	case ContentLeaf:
		if n.Value == nil {
			v.report(DiagContentModel, path, "%s requires a non-nil value", n.Type)
		}
		if len(n.Children) != 0 {
			v.report(DiagContentModel, path, "%s must not carry children", n.Type)
		}
	case ContentContainer:
		if n.Value != nil {
			v.report(DiagContentModel, path, "%s must not carry value", n.Type)
		}
		for i, c := range n.Children {
			if !v.childAllowed(spec.Children, c) {
				v.report(DiagContentModel, childPath(path, i),
					"%s must only contain %s (got %s)", n.Type, spec.Children, typeOf(c))
			}
		}
		if len(n.Children) < spec.MinChildren {
			v.report(DiagContentModel, path, "%s requires at least %d child (got %d)",
				n.Type, spec.MinChildren, len(n.Children))
		}
	}
}

// checkMeta 校验 meta 系统保留键与值类型（键按字典序遍历以保证消息确定）。
func (v *verifier) checkMeta(n *Node, path string) {
	if len(n.Meta) == 0 {
		return
	}
	keys := make([]string, 0, len(n.Meta))
	for k := range n.Meta {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		if isReservedMetaKey(k) {
			v.report(DiagMetaKey, path, "meta key %q is reserved by the node envelope", k)
			continue
		}
		if !isValidMetaValue(n.Meta[k]) {
			v.report(DiagMetaValue, path,
				"meta[%q] must be one of string | number | boolean | string[] (got %T)", k, n.Meta[k])
		}
	}
}

// reservedMetaKeys 是规范 §2.1.1 决策 3 划定的系统保留键集合。
var reservedMetaKeys = map[string]struct{}{
	"markdown-ir": {},
	"id":          {},
	"type":        {},
	"attrs":       {},
	"value":       {},
	"children":    {},
	"meta":        {},
	"fallback":    {},
	"pos":         {},
	"origin":      {},
	"errors":      {},
}

func isReservedMetaKey(k string) bool {
	if strings.HasPrefix(k, "_") || strings.HasPrefix(k, "sys:") {
		return true
	}
	_, reserved := reservedMetaKeys[k]
	return reserved
}

// isValidMetaValue 校验 meta 值属于 string | number | boolean | string[]；
// number 在数值规范化后为 int64/float64（兼容未经规范化的 int 与 json.Number），
// string[] 兼容 JSON 解码得到的 []any（全 string 元素）与手工构造的 []string。
func isValidMetaValue(v any) bool {
	switch t := v.(type) {
	case string, bool, int, int64, float64, json.Number:
		return true
	case []string:
		return true
	case []any:
		for _, e := range t {
			if _, ok := e.(string); !ok {
				return false
			}
		}
		return true
	default:
		return false
	}
}

func childPath(path string, i int) string    { return fmt.Sprintf("%s/children/%d", path, i) }
func fallbackPath(path string, i int) string { return fmt.Sprintf("%s/fallback/%d", path, i) }

func typeOf(n *Node) string {
	if n == nil {
		return "<nil>"
	}
	return fmt.Sprintf("%q", n.Type)
}

// ---------------------------------------------------------------------------
// 基础核规格表：与方言注册表同构的表驱动结构（规范 §2.1.3 两张指令表的一一映射），
// 通过 BaseSpec 暴露给方言作者复用与扩展。
// ---------------------------------------------------------------------------

var (
	rawBlockFormats  = []string{"markdown", "html", "ir-unknown", "invalid-x-node"}
	rawInlineFormats = []string{"markdown", "html", "ir-unknown"}
)

// noAttrs 是"无 attrs 且键封闭"的校验函数（doc/paragraph/text 等无属性节点共用）。
var noAttrs = AttrsValidator(AttrSchema{Closed: true})

func intPtr(i int) *int { return &i }

var baseSpecTable = map[string]NodeSpec{
	TypeDoc: {
		Type: TypeDoc, Block: true,
		Kind: ContentContainer, Children: ChildBlocks,
		ValidateAttrs: noAttrs,
	},
	TypeHeading: {
		Type: TypeHeading, Block: true,
		Kind: ContentContainer, Children: ChildInlines,
		ValidateAttrs: AttrsValidator(AttrSchema{
			Fields: map[string]AttrField{
				"depth": {Required: true, Type: AttrInt, MinInt: intPtr(1), MaxInt: intPtr(6)},
			},
			Closed: true,
		}),
	},
	TypeParagraph: {
		Type: TypeParagraph, Block: true,
		Kind: ContentContainer, Children: ChildInlines,
		ValidateAttrs: noAttrs,
	},
	TypeCodeBlock: {
		Type: TypeCodeBlock, Block: true,
		Kind: ContentLeaf, Children: ChildNone,
		ValidateAttrs: AttrsValidator(AttrSchema{
			Fields: map[string]AttrField{
				"lang": {Type: AttrString},
				"info": {Type: AttrString},
			},
			Closed: true,
		}),
	},
	TypeBlockquote: {
		Type: TypeBlockquote, Block: true,
		Kind: ContentContainer, Children: ChildBlocks,
		ValidateAttrs: noAttrs,
	},
	TypeList: {
		Type: TypeList, Block: true,
		Kind: ContentContainer, Children: ChildListItems, MinChildren: 1,
		ValidateAttrs: AttrsValidator(AttrSchema{
			Fields: map[string]AttrField{
				"ordered": {Type: AttrBool},
				"start":   {Type: AttrInt},
				"tight":   {Type: AttrBool},
			},
			Closed: true,
		}),
	},
	TypeListItem: {
		Type: TypeListItem, Block: true,
		Kind: ContentContainer, Children: ChildBlocks,
		ValidateAttrs: noAttrs,
	},
	TypeThematicBreak: {
		Type: TypeThematicBreak, Block: true,
		Kind: ContentEmpty, Children: ChildNone,
		ValidateAttrs: noAttrs,
	},
	TypeRawBlock: {
		Type: TypeRawBlock, Block: true,
		Kind: ContentLeaf, Children: ChildNone,
		ValidateAttrs: AttrsValidator(AttrSchema{
			Fields: map[string]AttrField{
				"format": {Required: true, Type: AttrString, Enum: rawBlockFormats},
			},
			Closed: true,
		}),
	},
	TypeText: {
		Type: TypeText,
		Kind: ContentLeaf, Children: ChildNone,
		ValidateAttrs: noAttrs,
	},
	TypeStrong: {
		Type: TypeStrong,
		Kind: ContentContainer, Children: ChildInlines, MinChildren: 1,
		ValidateAttrs: noAttrs,
	},
	TypeEmphasis: {
		Type: TypeEmphasis,
		Kind: ContentContainer, Children: ChildInlines, MinChildren: 1,
		ValidateAttrs: noAttrs,
	},
	TypeInlineCode: {
		Type: TypeInlineCode,
		Kind: ContentLeaf, Children: ChildNone,
		ValidateAttrs: noAttrs,
	},
	TypeLink: {
		Type: TypeLink,
		Kind: ContentContainer, Children: ChildInlines,
		ValidateAttrs: AttrsValidator(AttrSchema{
			Fields: map[string]AttrField{
				"url":   {Required: true, Type: AttrString},
				"title": {Type: AttrString},
			},
			Closed: true,
		}),
	},
	TypeImage: {
		Type: TypeImage,
		Kind: ContentEmpty, Children: ChildNone,
		ValidateAttrs: AttrsValidator(AttrSchema{
			Fields: map[string]AttrField{
				"url":   {Required: true, Type: AttrString},
				"alt":   {Required: true, Type: AttrString},
				"title": {Type: AttrString},
			},
			Closed: true,
		}),
	},
	TypeBreak: {
		Type: TypeBreak,
		Kind: ContentEmpty, Children: ChildNone,
		ValidateAttrs: AttrsValidator(AttrSchema{
			Fields: map[string]AttrField{
				"hard": {Type: AttrBool},
			},
			Closed: true,
		}),
	},
	TypeRawInline: {
		Type: TypeRawInline,
		Kind: ContentLeaf, Children: ChildNone,
		ValidateAttrs: AttrsValidator(AttrSchema{
			Fields: map[string]AttrField{
				"format": {Required: true, Type: AttrString, Enum: rawInlineFormats},
			},
			Closed: true,
		}),
	},
}

// BaseSpec 返回基础核节点类型的 NodeSpec（值拷贝），供方言注册表复用与扩展
// （例如 GFM 在 list_item 上叠加 checked 属性时，先取基础 spec 再替换 ValidateAttrs）。
func BaseSpec(nodeType string) (NodeSpec, bool) {
	spec, ok := baseSpecTable[nodeType]
	return spec, ok
}
