// Package statement normalizes user uploads, never posts money or proves bank facts.
package statement

import (
	"bytes"
	"crypto/sha256"
	"encoding/csv"
	"encoding/hex"
	"encoding/json"
	"github.com/qingyingjin/banking-service/internal/entity"
	"io"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
	_ "time/tzdata"
	"unicode/utf8"
)

const MaxCSVBytes = 256 * 1024
const MaxRecords = 1000

type Input struct {
	CSV      string            `json:"csv"`
	Timezone string            `json:"timezone"`
	Columns  map[string]string `json:"columns"`
}
type Source struct {
	Kind       string            `json:"kind"`
	FileSHA256 string            `json:"file_sha256"`
	RowSHA256  string            `json:"row_sha256"`
	RowNumber  int               `json:"row_number"`
	RawRecord  map[string]string `json:"raw_record"`
	Columns    map[string]string `json:"columns"`
	Timezone   string            `json:"timezone"`
}
type Record struct {
	ExternalTransactionID string `json:"external_transaction_id"`
	Direction             string `json:"direction"`
	AmountMinor           int64  `json:"amount_minor"`
	Currency              string `json:"currency"`
	Category              string `json:"category"`
	MerchantName          string `json:"merchant_name"`
	OccurredAt            int64  `json:"occurred_at"`
	EffectMode            string `json:"effect_mode"`
	Source                Source `json:"source"`
}
type Preview struct {
	FileSHA256                string   `json:"file_sha256"`
	Records                   []Record `json:"records"`
	Skipped                   int      `json:"skipped"`
	Warnings                  []string `json:"warnings"`
	FinancialExecutionEnabled bool     `json:"financial_execution_enabled"`
}

var decimal = regexp.MustCompile(`^[0-9]+(\.[0-9]{1,2})?$`)
var shaPattern = regexp.MustCompile(`^[0-9a-f]{64}$`)

func hash(raw []byte) string { h := sha256.Sum256(raw); return hex.EncodeToString(h[:]) }
func invalid() error {
	return entity.Fail("INVALID_ARGUMENT", "invalid CSV: require explicit field mapping, posted CNY external transactions, exact positive decimal amounts and valid timestamps; <=1000 rows/256KiB")
}

// Diagnostics name controlled schema fields and logical records, never values
// from the upload. A quoted field may span physical lines, so record numbers
// explicitly count the header as record one, as retained provenance already does.
func diagnosticMessage(record int, field, reason string) string {
	message := "CSV"
	if record > 0 {
		message += " 第 " + strconv.Itoa(record) + " 条记录（表头算第 1 条）"
	}
	labels := map[string]string{"external_id": "交易编号", "occurred_at": "交易时间", "amount": "金额", "direction": "收支方向", "currency": "币种", "status": "入账状态", "merchant": "商户名称", "category": "消费分类", "kind": "交易类型"}
	if label := labels[field]; label != "" {
		message += "的「" + label + "」"
	}
	return message + "：" + reason
}

func diagnostic(record int, field, reason string) error {
	return entity.Fail("INVALID_ARGUMENT", diagnosticMessage(record, field, reason))
}
func cents(raw string) (int64, error) {
	if !decimal.MatchString(raw) {
		return 0, invalid()
	}
	parts := strings.Split(raw, ".")
	whole, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil || whole > entity.MaxBalance/100 {
		return 0, invalid()
	}
	amount := whole * 100
	if len(parts) == 2 {
		fraction := parts[1]
		if len(fraction) == 1 {
			fraction += "0"
		}
		n, _ := strconv.ParseInt(fraction, 10, 64)
		amount += n
	}
	if amount <= 0 || amount > entity.MaxBalance {
		return 0, invalid()
	}
	return amount, nil
}
func ValidateSource(source *Source) error {
	if source == nil {
		return nil
	}
	if source.Kind != "unverified_csv_upload" || !shaPattern.MatchString(source.FileSHA256) || !shaPattern.MatchString(source.RowSHA256) || source.RowNumber < 2 || source.RowNumber > MaxRecords+1 || len(source.RawRecord) == 0 || len(source.RawRecord) > 32 || len(source.Columns) == 0 || len(source.Columns) > 9 || (source.Timezone != "UTC" && source.Timezone != "Asia/Shanghai") {
		return invalid()
	}
	raw, err := json.Marshal(source.RawRecord)
	if err != nil || len(raw) > 16*1024 || hash(raw) != source.RowSHA256 {
		return invalid()
	}
	return nil
}

// Re-normalize the retained row rather than trusting self-computable hashes.
// This proves internal traceability, never the upload's authenticity as bank data.
func ValidateRecord(record Record) error {
	if err := ValidateSource(&record.Source); err != nil {
		return err
	}
	headers := make([]string, 0, len(record.Source.RawRecord))
	for name := range record.Source.RawRecord {
		headers = append(headers, name)
	}
	sort.Strings(headers)
	values := make([]string, len(headers))
	for i, name := range headers {
		values[i] = record.Source.RawRecord[name]
	}
	var buffer bytes.Buffer
	writer := csv.NewWriter(&buffer)
	if err := writer.Write(headers); err != nil {
		return invalid()
	}
	if err := writer.Write(values); err != nil {
		return invalid()
	}
	writer.Flush()
	if writer.Error() != nil {
		return invalid()
	}
	parsed, err := Parse(Input{CSV: buffer.String(), Columns: record.Source.Columns, Timezone: record.Source.Timezone})
	if err != nil || len(parsed.Records) != 1 {
		return invalid()
	}
	expected := parsed.Records[0]
	expected.Source = Source{}
	record.Source = Source{}
	left, _ := json.Marshal(expected)
	right, _ := json.Marshal(record)
	if !bytes.Equal(left, right) {
		return entity.Fail("INVALID_ARGUMENT", "normalized transaction does not match retained upload row and mapping")
	}
	return nil
}
func Parse(in Input) (Preview, error) {
	out := Preview{Records: []Record{}, Warnings: []string{"用户上传记录未经银行核验；仅预览，不改变余额。导入须逐项进入独立授权网关。"}, FileSHA256: hash([]byte(in.CSV))}
	if len(in.CSV) == 0 || len(in.CSV) > MaxCSVBytes || !utf8.ValidString(in.CSV) {
		return out, diagnostic(0, "", "请提供非空 UTF-8 文件，大小不超过 256 KiB；不要直接上传其他编码文件。")
	}
	if in.Timezone != "UTC" && in.Timezone != "Asia/Shanghai" {
		return out, diagnostic(0, "", "时区必须明确选择 UTC 或 Asia/Shanghai。")
	}
	reader := csv.NewReader(strings.NewReader(strings.TrimPrefix(in.CSV, "\ufeff")))
	header, err := reader.Read()
	if err != nil || len(header) > 32 {
		return out, diagnostic(0, "", "表头格式无效；需使用逗号分隔，最多 32 列。")
	}
	positions := map[string]int{}
	for i, name := range header {
		name = strings.TrimSpace(name)
		if name == "" {
			return out, diagnostic(0, "", "表头不能为空。")
		}
		if _, exists := positions[name]; exists {
			return out, diagnostic(0, "", "表头不能重复；请先在本机核对列名。")
		}
		positions[name] = i
		header[i] = name
	}
	required := map[string]bool{"external_id": true, "occurred_at": true, "amount": true, "direction": true, "currency": true, "status": true, "merchant": false, "category": false, "kind": false}
	mapped := map[string]bool{}
	for field, name := range in.Columns {
		if _, ok := required[field]; !ok {
			return out, diagnostic(0, "", "字段映射包含不支持的项目。")
		}
		if _, ok := positions[name]; !ok || mapped[name] {
			return out, diagnostic(0, field, "字段映射的列不存在或已被其他字段使用，请重新匹配。")
		}
		mapped[name] = true
	}
	for field, must := range required {
		if must && in.Columns[field] == "" {
			return out, diagnostic(0, field, "字段映射缺少必填列，请明确选择。")
		}
	}
	location, _ := time.LoadLocation(in.Timezone)
	seen := map[string]string{}
	for index := 0; ; index++ {
		values, e := reader.Read()
		if e == io.EOF {
			break
		}
		if e != nil {
			return out, diagnostic(index+2, "", "格式无效，请核对引号及列数是否与表头一致。")
		}
		if index >= MaxRecords {
			return out, diagnostic(0, "", "最多允许 1000 条数据记录，请在本机按明确范围拆分后核对。")
		}
		get := func(field string) string {
			name, ok := in.Columns[field]
			if !ok {
				return ""
			}
			return strings.TrimSpace(values[positions[name]])
		}
		status := get("status")
		// Explicit equivalents only. A generic payment status such as "成功"
		// does not establish posting, and refunds must not be guessed as income.
		switch status {
		case "已入账":
			status = "posted"
		case "待入账":
			status = "pending"
		case "已冲正":
			status = "reversed"
		case "失败":
			status = "failed"
		}
		if status == "pending" || status == "reversed" || status == "failed" {
			out.Skipped++
			continue
		}
		if status != "posted" {
			return out, diagnostic(index+2, "status", "需明确为 posted 或已入账；pending、reversed、failed 及其中文等价状态会跳过，其他状态不推断为已入账。")
		}
		if get("kind") != "" && get("kind") != "external" {
			return out, diagnostic(index+2, "kind", "只接受 external 外部流水；请先排除账户内部划转，不能将其作为消费补录。")
		}
		at, e := time.Parse(time.RFC3339, get("occurred_at"))
		if e != nil {
			at, e = time.ParseInLocation("2006-01-02 15:04:05", get("occurred_at"), location)
		}
		if e != nil || at.Year() < 1971 || at.Year() > 2100 {
			return out, diagnostic(index+2, "occurred_at", "需为带时区 RFC3339 或 YYYY-MM-DD HH:mm:ss，年份范围为 1971 至 2100。")
		}
		amount, e := cents(get("amount"))
		if e != nil {
			return out, diagnostic(index+2, "amount", "需为大于零、最多两位小数的十进制元且不超过系统金额上限；不接受货币符号、千分位、负数或科学计数法。")
		}
		record := Record{ExternalTransactionID: get("external_id"), Direction: get("direction"), AmountMinor: amount, Currency: get("currency"), Category: get("category"), MerchantName: get("merchant"), OccurredAt: at.UnixMilli(), EffectMode: "record_only"}
		switch record.Direction {
		case "收入":
			record.Direction = "income"
		case "支出":
			record.Direction = "expense"
		}
		if record.Currency == "人民币" {
			record.Currency = "CNY"
		}
		if record.Category == "" {
			record.Category = "uncategorized"
		}
		if record.ExternalTransactionID == "" || len(record.ExternalTransactionID) > 128 {
			return out, diagnostic(index+2, "external_id", "需为非空、最多 128 字节的稳定交易编号；不能用行号代替。")
		}
		if len(record.MerchantName) > 128 {
			return out, diagnostic(index+2, "merchant", "最多 128 字节，请在本机核对。")
		}
		if len(record.Category) > 64 {
			return out, diagnostic(index+2, "category", "最多 64 字节，请在本机核对。")
		}
		if record.Currency != "CNY" {
			return out, diagnostic(index+2, "currency", "仅支持 CNY 或人民币；不会自动换汇或推断币种。")
		}
		if record.Direction != "income" && record.Direction != "expense" {
			return out, diagnostic(index+2, "direction", "需明确为 income / expense 或收入 / 支出；不自动推断退款方向。")
		}
		normalized, _ := json.Marshal(record)
		digest := hash(normalized)
		if prior, ok := seen[record.ExternalTransactionID]; ok {
			if prior != digest {
				return out, entity.Fail("IDEMPOTENCY_CONFLICT", diagnosticMessage(index+2, "external_id", "该编号在文件中已有不同业务内容，请核对原记录；不会覆盖或合并。"))
			}
			out.Skipped++
			continue
		}
		seen[record.ExternalTransactionID] = digest
		raw := map[string]string{}
		for i, name := range header {
			raw[name] = values[i]
		}
		encoded, _ := json.Marshal(raw)
		columns := map[string]string{}
		for name, value := range in.Columns {
			columns[name] = value
		}
		record.Source = Source{Kind: "unverified_csv_upload", FileSHA256: out.FileSHA256, RowSHA256: hash(encoded), RowNumber: index + 2, RawRecord: raw, Columns: columns, Timezone: in.Timezone}
		if e := ValidateSource(&record.Source); e != nil {
			return out, diagnostic(index+2, "", "原始来源记录不符合完整性或大小约束；单条来源 JSON 不得超过 16 KiB。")
		}
		out.Records = append(out.Records, record)
	}
	if out.Skipped > 0 {
		out.Warnings = append(out.Warnings, "已跳过未入账/冲销/失败及完全相同的重复记录；冲销不会自动反写旧交易。")
	}
	return out, nil
}
