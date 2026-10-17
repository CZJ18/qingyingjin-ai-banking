package storage

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"strings"
)

type cell struct {
	Kind  string `json:"kind"`
	Value string `json:"value,omitempty"`
}

func jsonCanonical(raw []byte) ([]byte, error) {
	var value any
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber() // Financial values and IDs must never round-trip through float64.
	if err := d.Decode(&value); err != nil {
		return nil, fmt.Errorf("invalid stored JSON")
	}
	var extra any
	if d.Decode(&extra) != io.EOF {
		return nil, fmt.Errorf("invalid trailing JSON")
	}
	return json.Marshal(value)
}

func kind(typ string) (string, error) {
	switch strings.ToLower(typ) {
	case "bigint", "integer", "int", "smallint":
		return "integer", nil
	case "tinyint", "boolean", "bool":
		return "boolean", nil
	case "json", "jsonb":
		return "json", nil
	case "longblob", "blob", "mediumblob", "binary", "varbinary", "bytea":
		return "binary", nil
	case "varchar", "char", "text", "longtext", "mediumtext", "character varying", "character":
		return "text", nil
	default:
		return "", fmt.Errorf("unsupported business column type %q", typ)
	}
}

func normalize(value any, columnKind string) (cell, any, error) {
	if value == nil {
		return cell{Kind: "null"}, nil, nil
	}
	var raw []byte
	switch v := value.(type) {
	case []byte:
		raw = v
	case string:
		raw = []byte(v)
	case int64:
		raw = []byte(strconv.FormatInt(v, 10))
	case bool:
		raw = []byte(strconv.FormatBool(v))
	default:
		return cell{}, nil, fmt.Errorf("unsupported scanned value type")
	}
	switch columnKind {
	case "integer":
		n, e := strconv.ParseInt(string(raw), 10, 64)
		if e != nil {
			return cell{}, nil, fmt.Errorf("invalid stored integer")
		}
		return cell{Kind: columnKind, Value: strconv.FormatInt(n, 10)}, n, nil
	case "boolean":
		var b bool
		switch string(raw) {
		case "1", "true":
			b = true
		case "0", "false":
		default:
			return cell{}, nil, fmt.Errorf("invalid stored boolean")
		}
		return cell{Kind: columnKind, Value: strconv.FormatBool(b)}, b, nil
	case "json":
		canonical, e := jsonCanonical(raw)
		if e != nil {
			return cell{}, nil, e
		}
		return cell{Kind: columnKind, Value: string(canonical)}, string(canonical), nil
	case "binary":
		copyValue := append([]byte(nil), raw...)
		return cell{Kind: columnKind, Value: base64.StdEncoding.EncodeToString(raw)}, copyValue, nil
	case "text":
		return cell{Kind: columnKind, Value: string(raw)}, string(raw), nil
	default:
		return cell{}, nil, fmt.Errorf("unsupported normalization kind")
	}
}
