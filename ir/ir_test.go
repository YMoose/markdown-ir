package ir

import (
	"bytes"
	"reflect"
	"strings"
	"testing"
)

// specExampleJSON 是规范 §2.1.6 的完整示例（逐字引用，作为权威正例语料）。
const specExampleJSON = `{
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
      "id": "20261029-cccc",
      "type": "list",
      "children": [
        {
          "id": "20261029-li01",
          "type": "list_item",
          "children": [
            {
              "id": "20261029-p001",
              "type": "paragraph",
              "children": [{ "type": "text", "value": "完成纯 IR 内核" }]
            }
          ]
        }
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
}`

// TestSpecExample verifies 规范 §2.1.6 完整示例在 RequireBlockID 模式下通过校验，且 R1 往返恒等。
func TestSpecExample(t *testing.T) {
	doc, err := UnmarshalIR([]byte(specExampleJSON))
	if err != nil {
		t.Fatalf("unmarshal spec example: %v", err)
	}
	if err := Verify(doc, VerifyOptions{RequireBlockID: true}); err != nil {
		t.Fatalf("spec §2.1.6 example must pass Verify with RequireBlockID=true:\n%v", err)
	}

	data, err := MarshalIR(doc)
	if err != nil {
		t.Fatalf("marshal spec example: %v", err)
	}
	doc2, err := UnmarshalIR(data)
	if err != nil {
		t.Fatalf("re-unmarshal spec example: %v", err)
	}
	if !reflect.DeepEqual(doc, doc2) {
		t.Fatalf("spec example R1 round-trip mismatch:\n got %+v\nwant %+v", doc2, doc)
	}
}

// buildRichDoc 用构造器搭一棵覆盖全部外壳字段与基础核节点的树。
func buildRichDoc() *Node {
	heading := NewHeading("h-1", 1, NewText("标题"))
	heading.Pos = &SourcePos{Start: 0, End: 42, Lines: []int{1}}
	heading.Origin = &SourceOrigin{RawBytes: "# 标题"}
	heading.Errors = []Diagnostic{{Kind: "test", Message: "m", Lines: []int{3}}}

	para := NewParagraph("p-1",
		NewText("a < b & c > d "),
		NewStrong(NewText("bold"), NewEmphasis(NewInlineCode("x<y"))),
		NewLink("https://example.com/?a=1&b=2", "", NewText("link")),
		NewLink("https://example.com/", "T & t", NewText("titled")),
		NewImage("https://example.com/i.png", "alt", ""),
		NewImage("https://example.com/i.png", "alt", "cap"),
		&Node{Type: TypeBreak, Attrs: map[string]any{"hard": true}},
		NewRawInline("html", "<br>&amp;"),
		&Node{
			Type: "x-wiki/link",
			Attrs: map[string]any{
				"target": "延迟优化方案",
				"alias":  "优化方案",
				"level":  int64(2),
			},
			Fallback: []*Node{NewText("[[延迟优化方案|优化方案]]")},
		},
	)
	para.Meta = map[string]any{"reviewed": true, "score": int64(5), "ratio": 0.5, "tags": []any{"a", "b"}}

	orderedList := &Node{
		ID:       "l-3",
		Type:     TypeList,
		Attrs:    map[string]any{"ordered": true, "start": int64(3), "tight": false},
		Children: []*Node{NewListItem("li-9", NewParagraph("p-9", NewText("ord")))},
	}

	doc := NewDoc("doc-1",
		heading,
		para,
		NewCodeBlock("cb-1", "go", "package main\n\nfunc main() {}\n"),
		NewCodeBlock("cb-2", "", "plain\ncode\n"),
		NewBlockquote("bq-1", NewParagraph("p-2", NewText("quoted"))),
		NewList("l-1",
			NewListItem("li-1", NewParagraph("p-3", NewText("item 1"))),
			NewListItem("li-2", NewList("l-2", NewListItem("li-3", NewParagraph("p-4", NewText("nested"))))),
		),
		orderedList,
		NewThematicBreak("tb-1"),
		NewRawBlock("rb-1", "html", "<div>keep <b>raw</b></div>"),
	)
	doc.Meta = map[string]any{"title": "测试文档", "count": int64(2), "ok": true, "tags": []any{"x"}}
	return doc
}

// TestConstructorRoundTrip 验证 构造器构树 → MarshalIR → UnmarshalIR → DeepEqual 相等，
// 且两次 MarshalIR 字节相等；数值（如 depth 1）往返后保持 int64。
func TestConstructorRoundTrip(t *testing.T) {
	doc := buildRichDoc()
	if err := Verify(doc, VerifyOptions{RequireBlockID: true}); err != nil {
		t.Fatalf("rich doc must verify (positive control):\n%v", err)
	}

	data1, err := MarshalIR(doc)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	data2, err := MarshalIR(doc)
	if err != nil {
		t.Fatalf("marshal again: %v", err)
	}
	if !bytes.Equal(data1, data2) {
		t.Fatalf("MarshalIR must be byte-deterministic:\n%s\n%s", data1, data2)
	}

	doc2, err := UnmarshalIR(data1)
	if err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !reflect.DeepEqual(doc, doc2) {
		t.Fatalf("R1 round-trip DeepEqual mismatch:\n got %+v\nwant %+v", doc2, doc)
	}

	if depth, ok := doc2.Children[0].Attrs["depth"].(int64); !ok || depth != 1 {
		t.Fatalf("attrs.depth must stay int64(1) after round-trip, got %T(%v)", doc2.Children[0].Attrs["depth"], doc2.Children[0].Attrs["depth"])
	}
}

// TestUnmarshalNumberNormalization 验证 Attrs/Meta（含 Fallback 子树与嵌套结构）中的
// json.Number 被规范化为 int64/float64，且二次往返类型稳定。
func TestUnmarshalNumberNormalization(t *testing.T) {
	raw := `{
		"markdown-ir": "0.0.1",
		"id": "d",
		"type": "doc",
		"meta": {"n": 1, "f": 1.5, "neg": -2, "big": 99999999999999999999},
		"children": [
			{"id": "h", "type": "heading", "attrs": {"depth": 2}, "children": [{"type": "text", "value": "t"}]},
			{"type": "x-cfg/node", "attrs": {"cfg": {"m": 3, "x": 0.25}, "arr": [1, 2.5]},
			 "fallback": [{"type": "text", "value": "f"}]}
		]
	}`
	doc, err := UnmarshalIR([]byte(raw))
	if err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if doc.Meta["n"] != any(int64(1)) {
		t.Fatalf("meta.n want int64(1), got %T(%v)", doc.Meta["n"], doc.Meta["n"])
	}
	if doc.Meta["f"] != any(float64(1.5)) {
		t.Fatalf("meta.f want float64(1.5), got %T(%v)", doc.Meta["f"], doc.Meta["f"])
	}
	if doc.Meta["neg"] != any(int64(-2)) {
		t.Fatalf("meta.neg want int64(-2), got %T(%v)", doc.Meta["neg"], doc.Meta["neg"])
	}
	if depth := doc.Children[0].Attrs["depth"]; depth != any(int64(2)) {
		t.Fatalf("attrs.depth want int64(2), got %T(%v)", depth, depth)
	}
	wantCfg := map[string]any{"m": int64(3), "x": float64(0.25)}
	if got := doc.Children[1].Attrs["cfg"]; !reflect.DeepEqual(got, wantCfg) {
		t.Fatalf("nested attrs cfg want %v, got %T(%v)", wantCfg, got, got)
	}
	wantArr := []any{int64(1), float64(2.5)}
	if got := doc.Children[1].Attrs["arr"]; !reflect.DeepEqual(got, wantArr) {
		t.Fatalf("attrs arr want %v, got %T(%v)", wantArr, got, got)
	}
	if doc.Meta["big"] != any(float64(99999999999999999999)) {
		t.Fatalf("meta.big (beyond int64 range) expected float64, got %T(%v)", doc.Meta["big"], doc.Meta["big"])
	}

	if err := Verify(doc, VerifyOptions{RequireBlockID: true}); err != nil {
		t.Fatalf("normalized doc must verify:\n%v", err)
	}

	data, err := MarshalIR(doc)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	doc2, err := UnmarshalIR(data)
	if err != nil {
		t.Fatalf("re-unmarshal: %v", err)
	}
	if !reflect.DeepEqual(doc, doc2) {
		t.Fatalf("number normalization must keep round-trip stable")
	}
}

// TestUnmarshalStrictness 验证 DisallowUnknownFields 与根文档严格性。
func TestUnmarshalStrictness(t *testing.T) {
	cases := []struct {
		name string
		raw  string
		want string
	}{
		{"unknown top-level field", `{"markdown-ir":"0.0.1","id":"d","type":"doc","extra":1}`, `unknown field "extra"`},
		{"unknown nested field", `{"markdown-ir":"0.0.1","id":"d","type":"doc","children":[{"type":"text","value":"x","typo":1}]}`, `unknown field "typo"`},
		{"trailing data", `{"markdown-ir":"0.0.1","id":"d","type":"doc"} trailing`, "trailing data"},
		{"null root", `null`, "must not be null"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := UnmarshalIR([]byte(tc.raw))
			if err == nil {
				t.Fatalf("expected error, got nil")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error %q missing substring %q", err, tc.want)
			}
		})
	}
}

// TestTypePredicates 验证块/行内/扩展类型判别函数的边界。
func TestTypePredicates(t *testing.T) {
	blocks := []string{TypeDoc, TypeHeading, TypeParagraph, TypeCodeBlock, TypeBlockquote,
		TypeList, TypeListItem, TypeThematicBreak, TypeRawBlock}
	inlines := []string{TypeText, TypeStrong, TypeEmphasis, TypeInlineCode,
		TypeLink, TypeImage, TypeBreak, TypeRawInline}
	for _, bt := range blocks {
		if !IsBlockType(bt) || IsInlineType(bt) {
			t.Errorf("IsBlockType(%q)=false or IsInlineType(%q)=true", bt, bt)
		}
	}
	for _, it := range inlines {
		if !IsInlineType(it) || IsBlockType(it) {
			t.Errorf("IsInlineType(%q)=false or IsBlockType(%q)=true", it, it)
		}
	}
	for _, xt := range []string{"x-wiki/link", "x-callout/block", "x-gfm/alert", "x-math/inline"} {
		if IsBlockType(xt) || IsInlineType(xt) {
			t.Errorf("x- type %q must be neither base-block nor base-inline", xt)
		}
	}
	extCases := map[string]bool{
		"x-wiki/link":   true,
		"x-gfm/alert":   true,
		"x-math/inline": true,
		"x-a-b/c-d":     true,
		"x-a/b":         true,
		"x-/b":          false,
		"x-a/":          false,
		"x-A/b":         false,
		"x-a/B":         false,
		"x-a/b/c":       false,
		"wiki/link":     false,
		"xa-/b":         false,
		"x-a/b c":       false,
	}
	for ext, want := range extCases {
		if got := IsExtensionType(ext); got != want {
			t.Errorf("IsExtensionType(%q) = %v, want %v", ext, got, want)
		}
	}
}

// TestVerifyViolations 是 Verifier 反例矩阵（PLAN Step 1.5 将扩充为 15 反例门禁的本步子集）。
func TestVerifyViolations(t *testing.T) {
	tests := []struct {
		name    string
		doc     *Node
		opts    VerifyOptions
		wantSub []string
	}{
		{
			name: "inline node carries id",
			doc: NewDoc("d", NewParagraph("p",
				&Node{ID: "t-1", Type: TypeText, Value: stringPtr("x")})),
			opts:    VerifyOptions{RequireBlockID: true},
			wantSub: []string{"id_boundary", "/children/0/children/0", "must not carry id"},
		},
		{
			name: "duplicate block id reports both paths",
			doc: NewDoc("d",
				NewParagraph("p", NewText("a")),
				NewParagraph("p", NewText("b"))),
			opts:    VerifyOptions{RequireBlockID: true},
			wantSub: []string{"duplicate_id", `duplicate id "p"`, "/children/1", `first seen at "/children/0"`},
		},
		{
			name:    "list directly contains paragraph",
			doc:     NewDoc("d", NewList("l", NewParagraph("p", NewText("x")))),
			opts:    VerifyOptions{RequireBlockID: true},
			wantSub: []string{"content_model", "/children/0/children/0", "list must only contain list_item"},
		},
		{
			name: "link nested inside link",
			doc: NewDoc("d", NewParagraph("p",
				NewLink("https://a", "", NewStrong(NewLink("https://b", "", NewText("x")))))),
			opts:    VerifyOptions{RequireBlockID: true},
			wantSub: []string{"content_model", "link must not be nested", "/children/0/children/0/children/0"},
		},
		{
			name:    "heading depth out of range",
			doc:     NewDoc("d", NewHeading("h", 7, NewText("x"))),
			opts:    VerifyOptions{RequireBlockID: true},
			wantSub: []string{"attr_schema", "depth", "range [1, 6]", "got 7"},
		},
		{
			name: "heading missing depth",
			doc: NewDoc("d", &Node{ID: "h", Type: TypeHeading,
				Children: []*Node{NewText("x")}}),
			opts:    VerifyOptions{RequireBlockID: true},
			wantSub: []string{"attr_schema", "attrs.depth", "required"},
		},
		{
			name: "heading depth non-integer",
			doc: NewDoc("d", &Node{ID: "h", Type: TypeHeading,
				Attrs:    map[string]any{"depth": 1.5},
				Children: []*Node{NewText("x")}}),
			opts:    VerifyOptions{RequireBlockID: true},
			wantSub: []string{"attr_schema", "depth", "must be an integer"},
		},
		{
			name: "meta contains reserved key type",
			doc: NewDoc("d", func() *Node {
				p := NewParagraph("p", NewText("x"))
				p.Meta = map[string]any{"type": "heading"}
				return p
			}()),
			opts:    VerifyOptions{RequireBlockID: true},
			wantSub: []string{"meta_key", `meta key "type" is reserved`},
		},
		{
			name: "meta contains underscore-prefixed key",
			doc: NewDoc("d", func() *Node {
				p := NewParagraph("p", NewText("x"))
				p.Meta = map[string]any{"_draft": true}
				return p
			}()),
			opts:    VerifyOptions{RequireBlockID: true},
			wantSub: []string{"meta_key", `meta key "_draft" is reserved`},
		},
		{
			name: "meta contains sys-prefixed key",
			doc: NewDoc("d", func() *Node {
				p := NewParagraph("p", NewText("x"))
				p.Meta = map[string]any{"sys:rev": "1"}
				return p
			}()),
			opts:    VerifyOptions{RequireBlockID: true},
			wantSub: []string{"meta_key", `meta key "sys:rev" is reserved`},
		},
		{
			name: "meta value is nested object",
			doc: NewDoc("d", func() *Node {
				p := NewParagraph("p", NewText("x"))
				p.Meta = map[string]any{"cfg": map[string]any{"a": 1}}
				return p
			}()),
			opts:    VerifyOptions{RequireBlockID: true},
			wantSub: []string{"meta_value", `meta["cfg"]`, "got map[string]interface"},
		},
		{
			name: "meta value is mixed array",
			doc: NewDoc("d", func() *Node {
				p := NewParagraph("p", NewText("x"))
				p.Meta = map[string]any{"tags": []any{"a", 1}}
				return p
			}()),
			opts:    VerifyOptions{RequireBlockID: true},
			wantSub: []string{"meta_value", `meta["tags"]`},
		},
		{
			name: "code_block carries children",
			doc: NewDoc("d", &Node{ID: "cb", Type: TypeCodeBlock,
				Attrs:    map[string]any{"lang": "go"},
				Value:    stringPtr("x"),
				Children: []*Node{NewText("y")}}),
			opts:    VerifyOptions{RequireBlockID: true},
			wantSub: []string{"content_model", "/children/0", "code_block must not carry children"},
		},
		{
			name:    "root version wrong",
			doc:     &Node{Version: "9.9.9", ID: "d", Type: TypeDoc},
			opts:    VerifyOptions{RequireBlockID: true},
			wantSub: []string{"root_envelope", `"0.0.1"`, `"9.9.9"`},
		},
		{
			name: "root type is not doc",
			doc: &Node{Version: IRVersion, ID: "d", Type: TypeParagraph,
				Children: []*Node{NewText("x")}},
			opts:    VerifyOptions{RequireBlockID: true},
			wantSub: []string{"root_envelope", `must have type "doc"`},
		},
		{
			name:    "non-root node carries version header",
			doc:     NewDoc("d", &Node{ID: "p", Type: TypeParagraph, Version: IRVersion, Children: []*Node{NewText("x")}}),
			opts:    VerifyOptions{RequireBlockID: true},
			wantSub: []string{"root_envelope", "only allowed on the root", "/children/0"},
		},
		{
			name: "list_item outside list",
			doc: NewDoc("d", NewBlockquote("b",
				NewListItem("li", NewParagraph("p", NewText("x"))))),
			opts:    VerifyOptions{RequireBlockID: true},
			wantSub: []string{"content_model", "list_item is only allowed as a direct child of list", `parent "blockquote"`},
		},
		{
			name:    "RequireBlockID missing block id",
			doc:     NewDoc("", NewParagraph("p", NewText("x"))),
			opts:    VerifyOptions{RequireBlockID: true},
			wantSub: []string{"id_boundary", `block node "doc" requires a non-empty id`},
		},
		{
			name:    "unknown node type not registered",
			doc:     NewDoc("d", &Node{ID: "t", Type: "table"}),
			opts:    VerifyOptions{RequireBlockID: true},
			wantSub: []string{"unknown_type", `unknown node type "table"`, "/children/0"},
		},
		{
			name: "link missing url",
			doc: NewDoc("d", NewParagraph("p",
				&Node{Type: TypeLink, Children: []*Node{NewText("x")}})),
			opts:    VerifyOptions{RequireBlockID: true},
			wantSub: []string{"attr_schema", "attrs.url", "required"},
		},
		{
			name:    "raw_block format outside enum",
			doc:     NewDoc("d", NewRawBlock("rb", "svg", "<svg/>")),
			opts:    VerifyOptions{RequireBlockID: true},
			wantSub: []string{"attr_schema", "attrs.format", "must be one of [markdown, html, ir-unknown, invalid-x-node]"},
		},
		{
			name: "paragraph carries unknown attr",
			doc: NewDoc("d", &Node{ID: "p", Type: TypeParagraph,
				Attrs:    map[string]any{"foo": true},
				Children: []*Node{NewText("x")}}),
			opts:    VerifyOptions{RequireBlockID: true},
			wantSub: []string{"attr_schema", "attrs.foo: unknown attribute"},
		},
		{
			name:    "empty list",
			doc:     NewDoc("d", NewList("l")),
			opts:    VerifyOptions{RequireBlockID: true},
			wantSub: []string{"content_model", "list requires at least 1 child"},
		},
		{
			name:    "strong with no children",
			doc:     NewDoc("d", NewParagraph("p", &Node{Type: TypeStrong})),
			opts:    VerifyOptions{RequireBlockID: true},
			wantSub: []string{"content_model", "strong requires at least 1 child"},
		},
		{
			name: "image with children",
			doc: NewDoc("d", NewParagraph("p",
				&Node{Type: TypeImage, Attrs: map[string]any{"url": "u", "alt": "a"},
					Children: []*Node{NewText("x")}})),
			opts:    VerifyOptions{RequireBlockID: true},
			wantSub: []string{"content_model", "image must not carry children"},
		},
		{
			name: "thematic_break carries value",
			doc: func() *Node {
				tb := NewThematicBreak("t")
				tb.Value = stringPtr("x")
				return NewDoc("d", tb)
			}(),
			opts:    VerifyOptions{RequireBlockID: true},
			wantSub: []string{"content_model", "thematic_break must not carry value"},
		},
		{
			name:    "text without value",
			doc:     NewDoc("d", NewParagraph("p", &Node{Type: TypeText})),
			opts:    VerifyOptions{RequireBlockID: true},
			wantSub: []string{"content_model", "text requires a non-nil value"},
		},
		{
			name:    "doc nested inside blockquote",
			doc:     NewDoc("d", NewBlockquote("b", NewDoc("d2"))),
			opts:    VerifyOptions{RequireBlockID: true},
			wantSub: []string{"content_model", `blockquote must only contain block nodes (excluding doc and list_item) (got "doc")`},
		},
		{
			name:    "base node with fallback",
			doc:     NewDoc("d", func() *Node { p := NewParagraph("p", NewText("x")); p.Fallback = []*Node{NewText("f")}; return p }()),
			opts:    VerifyOptions{RequireBlockID: true},
			wantSub: []string{"content_model", "paragraph must not carry fallback"},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := Verify(tc.doc, tc.opts)
			if err == nil {
				t.Fatalf("expected violation, Verify returned nil")
			}
			msg := err.Error()
			for _, sub := range tc.wantSub {
				if !strings.Contains(msg, sub) {
					t.Errorf("Verify error:\n%s\n  missing substring %q", msg, sub)
				}
			}
		})
	}
}

// TestVerifyAggregatesAllViolations 验证 Verifier 聚合报告而非遇错即停。
func TestVerifyAggregatesAllViolations(t *testing.T) {
	doc := NewDoc("d",
		NewHeading("h", 7, NewText("x")),                                         // attr_schema: depth 越界
		NewParagraph("p", &Node{ID: "t", Type: TypeText, Value: stringPtr("y")}), // id_boundary: 行内带 id
		NewParagraph("p", NewText("z")),                                          // duplicate_id: 与首个 p 冲突
	)
	err := Verify(doc, VerifyOptions{RequireBlockID: true})
	ve, ok := err.(*VerifyError)
	if !ok {
		t.Fatalf("Verify must return *VerifyError, got %T(%v)", err, err)
	}
	if len(ve.Diagnostics) != 3 {
		t.Fatalf("expected 3 aggregated diagnostics, got %d:\n%s", len(ve.Diagnostics), ve)
	}
	kinds := map[string]int{}
	for _, d := range ve.Diagnostics {
		kinds[d.Kind]++
	}
	for _, want := range []string{DiagAttrSchema, DiagIDBoundary, DiagDuplicateID} {
		if kinds[want] != 1 {
			t.Fatalf("expected exactly one %s diagnostic, got %d (all: %v)", want, kinds[want], ve)
		}
	}
}

// mapRegistry 是测试用最小 DialectRegistry 实现。
type mapRegistry map[string]NodeSpec

func (m mapRegistry) Lookup(nodeType string) (NodeSpec, bool) {
	spec, ok := m[nodeType]
	return spec, ok
}

// TestDialectRegistry 验证方言注册表机制：注册表优先于基础核表，
// 未注册 x- 节点按"基本外壳 + fallback 合法性"放行（规范 §2.1.4）。
func TestDialectRegistry(t *testing.T) {
	// GFM：在基础核 list_item 上叠加 checked 属性。
	gfmListItem, ok := BaseSpec(TypeListItem)
	if !ok {
		t.Fatalf("BaseSpec(list_item) missing")
	}
	gfmListItem.ValidateAttrs = AttrsValidator(AttrSchema{
		Fields: map[string]AttrField{"checked": {Type: AttrBool}},
		Closed: true,
	})
	// GFM：注册块级扩展节点 x-gfm/alert。
	gfmAlert := NodeSpec{
		Type:     "x-gfm/alert",
		Block:    true,
		Kind:     ContentContainer,
		Children: ChildBlocks,
		ValidateAttrs: AttrsValidator(AttrSchema{
			Fields: map[string]AttrField{"kind": {Required: true, Type: AttrString, Enum: []string{"note", "tip"}}},
			Closed: true,
		}),
	}
	reg := mapRegistry{TypeListItem: gfmListItem, "x-gfm/alert": gfmAlert}

	checkedItem := NewDoc("d", NewList("l",
		&Node{ID: "li", Type: TypeListItem, Attrs: map[string]any{"checked": true},
			Children: []*Node{NewParagraph("p", NewText("task"))}}))

	// 未注册方言时：list_item 的 attrs 封闭，checked 报未知属性。
	if err := Verify(checkedItem, VerifyOptions{RequireBlockID: true}); err == nil ||
		!strings.Contains(err.Error(), "attrs.checked: unknown attribute") {
		t.Fatalf("expected unknown attribute without registry, got %v", err)
	}
	// 注册后：checked 合法通过。
	if err := Verify(checkedItem, VerifyOptions{Registry: reg, RequireBlockID: true}); err != nil {
		t.Fatalf("checked list_item must pass with registry:\n%v", err)
	}

	noKind := NewDoc("d", &Node{ID: "al", Type: "x-gfm/alert",
		Children: []*Node{NewParagraph("p", NewText("hi"))}})
	// 未注册的 x- 节点：attrs 不约束（缺 kind 也放行）。
	if err := Verify(noKind, VerifyOptions{RequireBlockID: true}); err != nil {
		t.Fatalf("unregistered x- node must pass through:\n%v", err)
	}
	// 注册后：kind 必填被严格执行。
	if err := Verify(noKind, VerifyOptions{Registry: reg, RequireBlockID: true}); err == nil ||
		!strings.Contains(err.Error(), "attrs.kind: required") {
		t.Fatalf("expected missing attrs.kind with registry, got %v", err)
	}

	// 注册后块级归属生效：x-gfm/alert 放进 paragraph 应被 Content Model 拦截。
	misuse := NewDoc("d", NewParagraph("p",
		&Node{Type: "x-gfm/alert", Attrs: map[string]any{"kind": "note"}}))
	if err := Verify(misuse, VerifyOptions{Registry: reg, RequireBlockID: true}); err == nil ||
		!strings.Contains(err.Error(), `paragraph must only contain inline nodes (got "x-gfm/alert")`) {
		t.Fatalf("expected content model violation for block x- inside paragraph, got %v", err)
	}

	// 注册的块级扩展节点 fallback 必须为块级节点。
	badFallback := NewDoc("d", &Node{ID: "al", Type: "x-gfm/alert",
		Attrs:    map[string]any{"kind": "note"},
		Children: []*Node{NewParagraph("p", NewText("hi"))},
		Fallback: []*Node{NewText("inline fallback")}})
	if err := Verify(badFallback, VerifyOptions{Registry: reg, RequireBlockID: true}); err == nil ||
		!strings.Contains(err.Error(), `fallback of x-gfm/alert must contain block nodes`) {
		t.Fatalf("expected block-only fallback violation, got %v", err)
	}
}

// TestMarshalCanonicalForm 验证紧凑/缩进两种规范形与 HTML 转义关闭。
func TestMarshalCanonicalForm(t *testing.T) {
	doc := NewDoc("d", NewParagraph("p", NewText("a < b & c > d")))

	compact, err := MarshalIR(doc)
	if err != nil {
		t.Fatalf("MarshalIR: %v", err)
	}
	if !bytes.Contains(compact, []byte(`a < b & c > d`)) {
		t.Fatalf("HTML escaping must be disabled, got %s", compact)
	}
	if bytes.Contains(compact, []byte("\\n")) || bytes.Contains(compact, []byte("\n")) {
		t.Fatalf("MarshalIR must be compact (single line), got %s", compact)
	}

	indented, err := MarshalIRIndent(doc)
	if err != nil {
		t.Fatalf("MarshalIRIndent: %v", err)
	}
	if !strings.HasPrefix(string(indented), "{\n  \"markdown-ir\": \"0.0.1\"") {
		t.Fatalf("MarshalIRIndent must use two-space indent with envelope first, got %s", indented)
	}
	if !bytes.Contains(indented, []byte("a < b & c > d")) {
		t.Fatalf("MarshalIRIndent must also disable HTML escaping, got %s", indented)
	}

	// 两种形式反解后语义等价。
	a, err := UnmarshalIR(compact)
	if err != nil {
		t.Fatalf("unmarshal compact: %v", err)
	}
	b, err := UnmarshalIR(indented)
	if err != nil {
		t.Fatalf("unmarshal indented: %v", err)
	}
	if !reflect.DeepEqual(a, b) {
		t.Fatalf("compact and indented forms must decode to the same tree")
	}
}
