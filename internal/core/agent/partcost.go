package agent

import (
	"bytes"
	"compress/zlib"
	"io"

	"github.com/nigelteosw/eggy/internal/ports"
)

// pageChars is what one page image costs the budget: an attached image, or
// one page of a PDF. Providers bill attachments by what the model sees, not
// by file size -- Anthropic renders each PDF page as an image plus its text
// (typically 1,500-3,000 tokens of text per page, and an image is at most
// about 1,600 tokens), Gemini charges 258 tokens a page, and OpenRouter's
// native engine bills the provider's own input tokens. 8,000 characters is
// about 2,000 tokens, sized to the heavier of those rather than the lighter.
const pageChars = 8000

// assumedPageBytes turns a PDF's size into a page count when its page
// objects cannot be found. It is a guess, and exists so an unparseable
// document is still counted rather than slipping past the budget.
const assumedPageBytes = 32 << 10

// maxObjectStreamBytes bounds how much of one compressed object stream is
// inflated while looking for pages, so a hostile PDF cannot turn a budget
// check into a decompression bomb.
const maxObjectStreamBytes = 4 << 20

// partChars estimates what a non-text part costs a request. Counting the
// encoded bytes instead overstated a PDF tenfold or more -- most of a PDF is
// compressed fonts and images -- and refused documents the model would have
// read comfortably. A modality with no estimate is still counted by size.
func partChars(part ports.ContentPart) int {
	switch {
	case part.Type == ports.ModalityImage:
		return pageChars
	case part.Type == ports.ModalityFile && part.MediaType == "application/pdf":
		return pdfPages(part.Data) * pageChars
	default:
		return len(part.Data)
	}
}

// pdfPages counts a PDF's page objects. PDF 1.5 and later may pack them into
// compressed object streams, so those are inflated and counted too. It never
// reports fewer than one page.
func pdfPages(data []byte) int {
	pages := countPageObjects(data)
	for rest := data; ; {
		at := bytes.Index(rest, []byte("/ObjStm"))
		if at < 0 {
			break
		}
		rest = rest[at+len("/ObjStm"):]
		start := bytes.Index(rest, []byte("stream"))
		if start < 0 {
			break
		}
		body := bytes.TrimLeft(rest[start+len("stream"):], "\r\n")
		reader, err := zlib.NewReader(bytes.NewReader(body))
		if err != nil {
			continue
		}
		inflated, _ := io.ReadAll(io.LimitReader(reader, maxObjectStreamBytes))
		pages += countPageObjects(inflated)
	}
	if pages == 0 {
		pages = len(data) / assumedPageBytes
	}
	return max(pages, 1)
}

// countPageObjects counts "/Type /Page" dictionaries, leaving out the
// "/Type /Pages" tree nodes that share the prefix.
func countPageObjects(data []byte) int {
	count := 0
	for rest := data; ; {
		at := bytes.Index(rest, []byte("/Type"))
		if at < 0 {
			return count
		}
		rest = bytes.TrimLeft(rest[at+len("/Type"):], " \t\r\n")
		if !bytes.HasPrefix(rest, []byte("/Page")) {
			continue
		}
		rest = rest[len("/Page"):]
		if len(rest) == 0 || !isNameByte(rest[0]) {
			count++
		}
	}
}

func isNameByte(b byte) bool {
	return b >= 'a' && b <= 'z' || b >= 'A' && b <= 'Z' || b >= '0' && b <= '9'
}
