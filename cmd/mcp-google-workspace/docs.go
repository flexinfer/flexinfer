package main

import (
	"context"
	"strings"

	mcp "gitlab.flexinfer.ai/libs/mcp-go"
	docsapi "google.golang.org/api/docs/v1"

	"github.com/crb2nu/loom/pkg/strutil"
	"github.com/crb2nu/loom/pkg/validate"
)

func (s *googleWorkspaceServer) handleDocsGetDocument(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	v := validate.NewArgs(args)
	documentID := v.Required("document_id")
	if err := v.Validate(); err != nil {
		return mcp.ErrorResult(err), nil
	}

	ctx, cancel := context.WithTimeout(ctx, s.timeout)
	defer cancel()

	clients, _, err := s.newClients(ctx)
	if err != nil {
		return mcp.ErrorResult(err), nil
	}
	doc, err := clients.docs.Documents.Get(documentID).Do()
	if err != nil {
		return mcp.ErrorResult(s.wrapGoogleError("Docs", err)), nil
	}
	return mcp.JSONResult(simplifyDocument(doc))
}

func (s *googleWorkspaceServer) handleDocsCreateDocument(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	v := validate.NewArgs(args)
	title := v.Required("title")
	initialText := v.String("initial_text", "")
	if err := v.Validate(); err != nil {
		return mcp.ErrorResult(err), nil
	}

	ctx, cancel := context.WithTimeout(ctx, s.timeout)
	defer cancel()

	clients, _, err := s.newClients(ctx)
	if err != nil {
		return mcp.ErrorResult(err), nil
	}
	doc, err := clients.docs.Documents.Create(&docsapi.Document{Title: title}).Do()
	if err != nil {
		return mcp.ErrorResult(s.wrapGoogleError("Docs", err)), nil
	}
	if initialText != "" {
		if _, err := clients.docs.Documents.BatchUpdate(doc.DocumentId, &docsapi.BatchUpdateDocumentRequest{
			Requests: []*docsapi.Request{
				{
					InsertText: &docsapi.InsertTextRequest{
						Location: &docsapi.Location{Index: 1},
						Text:     initialText,
					},
				},
			},
		}).Do(); err != nil {
			return mcp.ErrorResult(s.wrapGoogleError("Docs", err)), nil
		}
		doc, err = clients.docs.Documents.Get(doc.DocumentId).Do()
		if err != nil {
			return mcp.ErrorResult(s.wrapGoogleError("Docs", err)), nil
		}
	}
	return mcp.JSONResult(simplifyDocument(doc))
}

func (s *googleWorkspaceServer) handleDocsAppendText(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	v := validate.NewArgs(args)
	documentID := v.Required("document_id")
	text := v.Required("text")
	if err := v.Validate(); err != nil {
		return mcp.ErrorResult(err), nil
	}

	ctx, cancel := context.WithTimeout(ctx, s.timeout)
	defer cancel()

	clients, _, err := s.newClients(ctx)
	if err != nil {
		return mcp.ErrorResult(err), nil
	}
	doc, err := clients.docs.Documents.Get(documentID).Do()
	if err != nil {
		return mcp.ErrorResult(s.wrapGoogleError("Docs", err)), nil
	}
	index := documentEndIndex(doc)
	if index < 1 {
		index = 1
	}
	if _, err := clients.docs.Documents.BatchUpdate(documentID, &docsapi.BatchUpdateDocumentRequest{
		Requests: []*docsapi.Request{
			{
				InsertText: &docsapi.InsertTextRequest{
					Location: &docsapi.Location{Index: index},
					Text:     text,
				},
			},
		},
	}).Do(); err != nil {
		return mcp.ErrorResult(s.wrapGoogleError("Docs", err)), nil
	}
	doc, err = clients.docs.Documents.Get(documentID).Do()
	if err != nil {
		return mcp.ErrorResult(s.wrapGoogleError("Docs", err)), nil
	}
	return mcp.JSONResult(simplifyDocument(doc))
}

func simplifyDocument(doc *docsapi.Document) map[string]any {
	return map[string]any{
		"document_id": doc.DocumentId,
		"title":       doc.Title,
		"revision_id": doc.RevisionId,
		"url":         "https://docs.google.com/document/d/" + doc.DocumentId + "/edit",
		"text":        strutil.Truncate(documentText(doc), 12000),
	}
}

func documentText(doc *docsapi.Document) string {
	if doc == nil || doc.Body == nil {
		return ""
	}
	var parts []string
	for _, content := range doc.Body.Content {
		if content.Paragraph == nil {
			continue
		}
		for _, elem := range content.Paragraph.Elements {
			if elem.TextRun == nil {
				continue
			}
			parts = append(parts, elem.TextRun.Content)
		}
	}
	return strings.TrimSpace(strings.Join(parts, ""))
}

func documentEndIndex(doc *docsapi.Document) int64 {
	if doc == nil || doc.Body == nil || len(doc.Body.Content) == 0 {
		return 1
	}
	last := doc.Body.Content[len(doc.Body.Content)-1]
	if last.EndIndex <= 1 {
		return 1
	}
	return last.EndIndex - 1
}
