package importcsv

import (
	"archive/zip"
	"bytes"
	"encoding/xml"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

func isZip(data []byte) bool {
	return len(data) >= 4 && data[0] == 'P' && data[1] == 'K' && data[2] == 3 && data[3] == 4
}

var sheetNameRe = regexp.MustCompile(`^xl/worksheets/sheet\d+\.xml$`)

func xlsxTables(data []byte) ([]table, error) {
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return nil, fmt.Errorf("не xlsx: %w", err)
	}
	shared, err := sharedStrings(zr)
	if err != nil {
		return nil, err
	}
	var names []string
	for _, f := range zr.File {
		if sheetNameRe.MatchString(f.Name) {
			names = append(names, f.Name)
		}
	}
	sort.Strings(names)
	var tables []table
	for _, n := range names {
		t, err := sheetTable(zr, n, shared)
		if err != nil {
			return nil, err
		}
		if len(t.headers) > 0 {
			tables = append(tables, t)
		}
	}
	if len(tables) == 0 {
		return nil, fmt.Errorf("пустой xlsx: нет листов с данными")
	}
	return tables, nil
}

func sharedStrings(zr *zip.Reader) ([]string, error) {
	f, err := zr.Open("xl/sharedStrings.xml")
	if err != nil {
		return nil, nil // файл опционален
	}
	defer f.Close()
	var doc struct {
		SI []struct {
			T   string `xml:"t"`
			R   []struct {
				T string `xml:"t"`
			} `xml:"r"`
		} `xml:"si"`
	}
	if err := xml.NewDecoder(f).Decode(&doc); err != nil {
		return nil, fmt.Errorf("xlsx: битый словарь строк: %w", err)
	}
	out := make([]string, len(doc.SI))
	for i, si := range doc.SI {
		var b strings.Builder
		b.WriteString(si.T)
		for _, r := range si.R {
			b.WriteString(r.T)
		}
		out[i] = b.String()
	}
	return out, nil
}

func sheetTable(zr *zip.Reader, name string, shared []string) (table, error) {
	f, err := zr.Open(name)
	if err != nil {
		return table{}, err
	}
	defer f.Close()
	var doc struct {
		SheetData struct {
			Rows []struct {
				Cells []struct {
					Ref  string `xml:"r,attr"`
					Type string `xml:"t,attr"`
					V    string `xml:"v"`
					IS   struct {
						T string `xml:"t"`
						R []struct {
							T string `xml:"t"`
						} `xml:"r"`
					} `xml:"is"`
				} `xml:"c"`
			} `xml:"row"`
		} `xml:"sheetData"`
	}
	if err := xml.NewDecoder(f).Decode(&doc); err != nil {
		return table{}, fmt.Errorf("xlsx: битый лист %s: %w", name, err)
	}

	var t table
	seq := 0
	for _, row := range doc.SheetData.Rows {
		var vals []string
		for _, c := range row.Cells {
			idx := colIdx(c.Ref)
			if idx < 0 {
				idx = seq
			}
			seq = idx + 1
			for len(vals) <= idx {
				vals = append(vals, "")
			}
			switch c.Type {
			case "s":
				n, err := strconv.Atoi(strings.TrimSpace(c.V))
				if err == nil && n >= 0 && n < len(shared) {
					vals[idx] = shared[n]
				}
			case "inlineStr":
				vals[idx] = c.IS.T
				if vals[idx] == "" {
					for _, r := range c.IS.R {
						vals[idx] += r.T
					}
				}
			default:
				vals[idx] = c.V
			}
		}
		seq = 0
		if emptyRow(vals) {
			continue
		}
		if t.headers == nil {
			t.headers = vals
			continue
		}
		t.rows = append(t.rows, vals)
	}
	return t, nil
}

func colIdx(ref string) int {
	n := 0
	for _, ch := range ref {
		switch {
		case ch >= 'A' && ch <= 'Z':
			n = n*26 + int(ch-'A') + 1
		case ch >= 'a' && ch <= 'z':
			n = n*26 + int(ch-'a') + 1
		default:
			if n == 0 {
				return -1
			}
			return n - 1
		}
	}
	if n == 0 {
		return -1
	}
	return n - 1
}

func emptyRow(vals []string) bool {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return false
		}
	}
	return true
}
