package ebook

import (
	"archive/zip"
	"encoding/xml"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// Meta holds auto-detected metadata for an e-book file.
type Meta struct {
	Title  string
	Author string
	Format string
}

// Extract best-effort reads metadata from a local e-book file.
func Extract(path string) Meta {
	format := strings.TrimPrefix(strings.ToLower(filepath.Ext(path)), ".")
	m := Meta{Format: format}
	switch format {
	case "epub":
		m.Title, m.Author = epubMeta(path)
	case "pdf":
		m.Title, m.Author = pdfMeta(path)
	}
	if m.Title == "" {
		m.Title = strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
	}
	return m
}

func epubMeta(path string) (string, string) {
	zr, err := zip.OpenReader(path)
	if err != nil {
		return "", ""
	}
	defer zr.Close()

	// locate the OPF via META-INF/container.xml
	var opfPath string
	for _, f := range zr.File {
		if f.Name == "META-INF/container.xml" {
			rc, _ := f.Open()
			var c struct {
				Rootfiles []struct {
					FullPath string `xml:"full-path,attr"`
				} `xml:"rootfiles>rootfile"`
			}
			_ = xml.NewDecoder(rc).Decode(&c)
			rc.Close()
			if len(c.Rootfiles) > 0 {
				opfPath = c.Rootfiles[0].FullPath
			}
		}
	}
	if opfPath == "" {
		for _, f := range zr.File {
			if strings.HasSuffix(f.Name, ".opf") {
				opfPath = f.Name
				break
			}
		}
	}
	if opfPath == "" {
		return "", ""
	}
	for _, f := range zr.File {
		if f.Name == opfPath {
			rc, _ := f.Open()
			title, author := parseOpf(rc)
			rc.Close()
			return title, author
		}
	}
	return "", ""
}

// parseOpf scans the OPF XML for dc:title and dc:creator.
func parseOpf(r io.Reader) (string, string) {
	dec := xml.NewDecoder(r)
	var title, author string
	for {
		tok, err := dec.Token()
		if err != nil {
			break
		}
		if se, ok := tok.(xml.StartElement); ok {
			switch se.Name.Local {
			case "title":
				if title == "" {
					if d, ok := nextData(dec); ok {
						title = strings.TrimSpace(string(d))
					}
				}
			case "creator":
				if author == "" {
					if d, ok := nextData(dec); ok {
						author = strings.TrimSpace(string(d))
					}
				}
			}
		}
	}
	return title, author
}

func nextData(dec *xml.Decoder) (string, bool) {
	for {
		tok, err := dec.Token()
		if err != nil {
			return "", false
		}
		if cd, ok := tok.(xml.CharData); ok && strings.TrimSpace(string(cd)) != "" {
			return string(cd), true
		}
		if _, ok := tok.(xml.EndElement); ok {
			return "", false
		}
	}
}

var (
	reTitle  = regexp.MustCompile(`/Title\s*\(([^)]*)\)`)
	reAuthor = regexp.MustCompile(`/Author\s*\(([^)]*)\)`)
)

func pdfMeta(path string) (string, string) {
	f, err := os.Open(path)
	if err != nil {
		return "", ""
	}
	defer f.Close()
	buf := make([]byte, 1<<20)
	n, _ := f.Read(buf)
	s := string(buf[:n])
	var title, author string
	if m := reTitle.FindStringSubmatch(s); len(m) > 1 {
		title = unescapePDF(m[1])
	}
	if m := reAuthor.FindStringSubmatch(s); len(m) > 1 {
		author = unescapePDF(m[1])
	}
	return title, author
}

func unescapePDF(s string) string {
	r := strings.NewReplacer(`\(`, `(`, `\)`, `)`, `\\`, `\`)
	return strings.TrimSpace(r.Replace(s))
}
