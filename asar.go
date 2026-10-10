// asar 读写:Chromium Pickle 头 + JSON 索引 + 文件数据区。
//
// 关键约束(血泪教训,勿改):
//   - 数据区起点 = 16 字节 Pickle 头 + JSON 长度按 4 字节对齐后的位置;
//   - JSON 索引必须"保序 + 紧凑 + ASCII 转义"重新生成,
//     否则无法通过"解包 -> 重打包 必须与官方逐字节一致"的自检。
package main

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"os"
	"strconv"
)

// ---------------------------------------------------------------- 保序 JSON

// jsonObject 保持键的插入顺序(asar 索引与词典都依赖此特性)。
type jsonObject struct {
	keys []string
	vals map[string]interface{}
}

func newJSONObject() *jsonObject {
	return &jsonObject{vals: map[string]interface{}{}}
}

func (o *jsonObject) Set(k string, v interface{}) {
	if _, exists := o.vals[k]; !exists {
		o.keys = append(o.keys, k)
	}
	o.vals[k] = v
}

func (o *jsonObject) Get(k string) (interface{}, bool) {
	v, ok := o.vals[k]
	return v, ok
}

func (o *jsonObject) GetString(k string) string {
	if v, ok := o.vals[k]; ok {
		if s, ok := v.(string); ok {
			return s
		}
	}
	return ""
}

// parseOrderedJSON 用流式解析保持键顺序;数字保留原始文本(json.Number)。
func parseOrderedJSON(data []byte) (interface{}, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	return parseJSONValue(dec)
}

func parseJSONValue(dec *json.Decoder) (interface{}, error) {
	t, err := dec.Token()
	if err != nil {
		return nil, err
	}
	return parseJSONFromToken(dec, t)
}

func parseJSONFromToken(dec *json.Decoder, t json.Token) (interface{}, error) {
	switch v := t.(type) {
	case json.Delim:
		switch v {
		case '{':
			obj := newJSONObject()
			for dec.More() {
				kt, err := dec.Token()
				if err != nil {
					return nil, err
				}
				key, _ := kt.(string)
				val, err := parseJSONValue(dec)
				if err != nil {
					return nil, err
				}
				obj.Set(key, val)
			}
			if _, err := dec.Token(); err != nil { // 收尾 '}'
				return nil, err
			}
			return obj, nil
		case '[':
			arr := []interface{}{}
			for dec.More() {
				val, err := parseJSONValue(dec)
				if err != nil {
					return nil, err
				}
				arr = append(arr, val)
			}
			if _, err := dec.Token(); err != nil { // 收尾 ']'
				return nil, err
			}
			return arr, nil
		}
		return nil, fmt.Errorf("不支持的 JSON 结构: %v", v)
	case string, json.Number, bool, nil:
		return v, nil
	}
	return nil, fmt.Errorf("不支持的 JSON 值: %T", t)
}

// marshalOrderedJSON 生成与 Python json.dumps(separators=(',', ':'), ensure_ascii=True) 等价的紧凑 JSON。
func marshalOrderedJSON(v interface{}) []byte {
	var buf bytes.Buffer
	appendJSONValue(&buf, v)
	return buf.Bytes()
}

func appendJSONValue(buf *bytes.Buffer, v interface{}) {
	switch t := v.(type) {
	case *jsonObject:
		buf.WriteByte('{')
		for i, k := range t.keys {
			if i > 0 {
				buf.WriteByte(',')
			}
			appendJSONString(buf, k)
			buf.WriteByte(':')
			appendJSONValue(buf, t.vals[k])
		}
		buf.WriteByte('}')
	case []interface{}:
		buf.WriteByte('[')
		for i, e := range t {
			if i > 0 {
				buf.WriteByte(',')
			}
			appendJSONValue(buf, e)
		}
		buf.WriteByte(']')
	case string:
		appendJSONString(buf, t)
	case json.Number:
		buf.WriteString(string(t))
	case bool:
		if t {
			buf.WriteString("true")
		} else {
			buf.WriteString("false")
		}
	case int:
		buf.WriteString(strconv.Itoa(t))
	case int64:
		buf.WriteString(strconv.FormatInt(t, 10))
	case nil:
		buf.WriteString("null")
	default:
		panic(fmt.Sprintf("appendJSONValue: 不支持的类型 %T", v))
	}
}

// appendJSONString 逐字符转义,规则与 Python json.dumps(ensure_ascii=True) 一致。
func appendJSONString(buf *bytes.Buffer, s string) {
	buf.WriteByte('"')
	for _, r := range s {
		switch r {
		case '"':
			buf.WriteString(`\"`)
		case '\\':
			buf.WriteString(`\\`)
		case '\b':
			buf.WriteString(`\b`)
		case '\f':
			buf.WriteString(`\f`)
		case '\n':
			buf.WriteString(`\n`)
		case '\r':
			buf.WriteString(`\r`)
		case '\t':
			buf.WriteString(`\t`)
		default:
			if r < 0x20 || r >= 0x80 {
				if r > 0xFFFF {
					hi := 0xD800 + ((r - 0x10000) >> 10)
					lo := 0xDC00 + ((r - 0x10000) & 0x3FF)
					fmt.Fprintf(buf, `\u%04x\u%04x`, hi, lo)
				} else {
					fmt.Fprintf(buf, `\u%04x`, r)
				}
			} else {
				buf.WriteRune(r)
			}
		}
	}
	buf.WriteByte('"')
}

// ---------------------------------------------------------------- 数值读取

func jsonInt(v interface{}) (int64, bool) {
	switch t := v.(type) {
	case json.Number:
		n, err := t.Int64()
		return n, err == nil
	case string:
		n, err := strconv.ParseInt(t, 10, 64)
		return n, err == nil
	case float64:
		return int64(t), true
	}
	return 0, false
}

func jsonTruthy(v interface{}) bool {
	switch t := v.(type) {
	case bool:
		return t
	case nil:
		return false
	case string:
		return t != ""
	case json.Number:
		n, _ := t.Int64()
		return n != 0
	}
	return true
}

// ---------------------------------------------------------------- asar 解包/打包

type asarFile struct {
	info    *jsonObject
	content []byte
}

// unpackAsar 返回(索引树, 路径 -> 内容)。
func unpackAsar(asarPath string) (*jsonObject, map[string][]byte, error) {
	data, err := os.ReadFile(asarPath)
	if err != nil {
		return nil, nil, err
	}
	if len(data) < 16 {
		return nil, nil, fmt.Errorf("asar 文件过小")
	}
	jsonLen := int(binary.LittleEndian.Uint32(data[12:16]))
	if jsonLen <= 0 || 16+jsonLen > len(data) {
		return nil, nil, fmt.Errorf("asar 索引长度异常: %d", jsonLen)
	}
	parsed, err := parseOrderedJSON(data[16 : 16+jsonLen])
	if err != nil {
		return nil, nil, fmt.Errorf("asar 索引解析失败: %w", err)
	}
	header, ok := parsed.(*jsonObject)
	if !ok {
		return nil, nil, fmt.Errorf("asar 索引不是对象结构")
	}
	// 数据区起点 = 16 字节 Pickle 头 + JSON 长度按 4 字节对齐后的位置。
	// 官方 asar 的 JSON 长度不一定整除 4(存在 1~3 字节填充),
	// 直接按 jsonLen 计算会整体错位,导致所有文件多读填充字节、末尾截断。
	base := 16 + jsonLen + (4-jsonLen%4)%4

	files := map[string][]byte{}
	var walk func(node *jsonObject, prefix string) error
	walk = func(node *jsonObject, prefix string) error {
		filesNode, ok := node.Get("files")
		if !ok {
			return nil
		}
		filesObj, ok := filesNode.(*jsonObject)
		if !ok {
			return nil
		}
		for _, name := range filesObj.keys {
			info, ok := filesObj.vals[name].(*jsonObject)
			if !ok {
				continue
			}
			sub := name
			if prefix != "" {
				sub = prefix + "/" + name
			}
			if _, isDir := info.Get("files"); isDir {
				if err := walk(info, sub); err != nil {
					return err
				}
				continue
			}
			if _, isLink := info.Get("link"); isLink {
				continue
			}
			if v, ok := info.Get("unpacked"); ok && jsonTruthy(v) {
				continue
			}
			offset, ok1 := jsonInt(info.vals["offset"])
			size, ok2 := jsonInt(info.vals["size"])
			if !ok1 || !ok2 {
				return fmt.Errorf("asar 条目缺少 offset/size: %s", sub)
			}
			if base+int(offset)+int(size) > len(data) {
				return fmt.Errorf("asar 条目越界: %s", sub)
			}
			files[sub] = data[base+int(offset) : base+int(offset)+int(size)]
		}
		return nil
	}
	if err := walk(header, ""); err != nil {
		return nil, nil, err
	}
	return header, files, nil
}

// packAsar 按 Chromium Pickle 规范重新打包(官方原样重打包必须逐字节一致)。
func packAsar(header *jsonObject, files map[string][]byte, outPath string) error {
	var payloads []asarFile
	currentOffset := 0

	var walk func(node *jsonObject, prefix string) error
	walk = func(node *jsonObject, prefix string) error {
		filesNode, ok := node.Get("files")
		if !ok {
			return nil
		}
		filesObj, ok := filesNode.(*jsonObject)
		if !ok {
			return nil
		}
		for _, name := range filesObj.keys {
			info, ok := filesObj.vals[name].(*jsonObject)
			if !ok {
				continue
			}
			sub := name
			if prefix != "" {
				sub = prefix + "/" + name
			}
			if _, isDir := info.Get("files"); isDir {
				if err := walk(info, sub); err != nil {
					return err
				}
				continue
			}
			if _, isLink := info.Get("link"); isLink {
				continue
			}
			if v, ok := info.Get("unpacked"); ok && jsonTruthy(v) {
				continue
			}
			content, ok := files[sub]
			if !ok {
				return fmt.Errorf("缺少文件数据: %s", sub)
			}
			info.Set("size", json.Number(strconv.Itoa(len(content))))
			info.Set("offset", strconv.Itoa(currentOffset))
			payloads = append(payloads, asarFile{info: info, content: content})
			currentOffset += len(content)
		}
		return nil
	}
	if err := walk(header, ""); err != nil {
		return err
	}

	headerBytes := marshalOrderedJSON(header)
	jsonLen := len(headerBytes)
	pad := (4 - jsonLen%4) % 4
	aligned := jsonLen + pad

	// 遵循 Chromium Pickle 规范:[4, aligned+8, aligned+4, json_len]
	pickle := make([]byte, 16)
	binary.LittleEndian.PutUint32(pickle[0:], 4)
	binary.LittleEndian.PutUint32(pickle[4:], uint32(aligned+8))
	binary.LittleEndian.PutUint32(pickle[8:], uint32(aligned+4))
	binary.LittleEndian.PutUint32(pickle[12:], uint32(jsonLen))

	var out bytes.Buffer
	out.Grow(16 + aligned + currentOffset)
	out.Write(pickle)
	out.Write(headerBytes)
	out.Write(make([]byte, pad))
	for _, p := range payloads {
		out.Write(p.content)
	}
	return os.WriteFile(outPath, out.Bytes(), 0o644)
}
