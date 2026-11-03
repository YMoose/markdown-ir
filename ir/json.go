package ir

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
)

// UnmarshalIR 严格解码 IR JSON（规范 §2.1.1 决策 2）：
//   - 使用 DisallowUnknownFields 拦截一切逃出固定外壳的未知顶层字段，绝不静默丢弃；
//   - 使用 UseNumber + 递归数值规范化，将 Attrs/Meta（含 Fallback 子树）中的
//     json.Number 转为 int64（整数值）或 float64（非整数值），保证 R1 往返
//     reflect.DeepEqual 稳定。
func UnmarshalIR(data []byte) (*Node, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	dec.DisallowUnknownFields()
	var root *Node
	if err := dec.Decode(&root); err != nil {
		return nil, fmt.Errorf("markdown-ir: decode IR JSON: %w", err)
	}
	if root == nil {
		return nil, fmt.Errorf("markdown-ir: IR JSON root must not be null")
	}
	if _, err := dec.Token(); err != io.EOF {
		return nil, fmt.Errorf("markdown-ir: trailing data after IR JSON root: %v", err)
	}
	normalizeNumbers(root)
	return root, nil
}

// MarshalIR 将 IR 树序列化为紧凑规范形：关闭 HTML 转义、顶层字段按外壳声明序、
// Attrs/Meta 等 map 键由 encoding/json 自动按字典序输出，结果确定可复现。
func MarshalIR(n *Node) ([]byte, error) {
	return marshalIR(n, "")
}

// MarshalIRIndent 将 IR 树序列化为两空格缩进的规范形（规范形约束同 MarshalIR）。
func MarshalIRIndent(n *Node) ([]byte, error) {
	return marshalIR(n, "  ")
}

func marshalIR(n *Node, indent string) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if indent != "" {
		enc.SetIndent("", indent)
	}
	if err := enc.Encode(n); err != nil {
		return nil, fmt.Errorf("markdown-ir: encode IR JSON: %w", err)
	}
	return bytes.TrimSuffix(buf.Bytes(), []byte("\n")), nil
}

// normalizeNumbers 就地规范化整棵树（含 Fallback 子树）中 Attrs/Meta 的数值：
// json.Number 的整数值转 int64，非整数值转 float64；嵌套对象与数组中的数值同样处理。
// 超出 int64 范围的整数退化为 float64（JSON 数字本就以 double 语义为最终基准）。
func normalizeNumbers(n *Node) {
	if n == nil {
		return
	}
	n.Attrs = normalizeNumberMap(n.Attrs)
	n.Meta = normalizeNumberMap(n.Meta)
	for _, c := range n.Children {
		normalizeNumbers(c)
	}
	for _, f := range n.Fallback {
		normalizeNumbers(f)
	}
}

func normalizeNumberMap(m map[string]any) map[string]any {
	if m == nil {
		return nil
	}
	for k, v := range m {
		m[k] = normalizeNumberValue(v)
	}
	return m
}

func normalizeNumberValue(v any) any {
	switch t := v.(type) {
	case json.Number:
		if i, err := t.Int64(); err == nil {
			return i
		}
		if f, err := t.Float64(); err == nil {
			return f
		}
		return t // 不可解析的数字原样保留，交由 Verify 报告
	case map[string]any:
		for k, vv := range t {
			t[k] = normalizeNumberValue(vv)
		}
		return t
	case []any:
		for i, vv := range t {
			t[i] = normalizeNumberValue(vv)
		}
		return t
	default:
		return v
	}
}
