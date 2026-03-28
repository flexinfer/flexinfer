package main

import (
	"context"
	"fmt"
	"strings"

	mcp "gitlab.flexinfer.ai/libs/mcp-go"
	drive "google.golang.org/api/drive/v3"

	"github.com/crb2nu/loom/pkg/validate"
)

func (s *googleWorkspaceServer) handleDriveSearchFiles(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	v := validate.NewArgs(args)
	query := v.String("query", "")
	mimeType := v.String("mime_type", "")
	maxResults := validate.NormalizePerPage(v.Int("max_results", 20), 20, 50)
	pageToken := v.String("page_token", "")
	if err := v.Validate(); err != nil {
		return mcp.ErrorResult(err), nil
	}

	ctx, cancel := context.WithTimeout(ctx, s.timeout)
	defer cancel()

	clients, _, err := s.newClients(ctx)
	if err != nil {
		return mcp.ErrorResult(err), nil
	}

	driveQuery := strings.TrimSpace(query)
	if mimeType != "" {
		filter := fmt.Sprintf("mimeType = '%s'", strings.ReplaceAll(mimeType, "'", "\\'"))
		if driveQuery == "" {
			driveQuery = filter
		} else {
			driveQuery = fmt.Sprintf("(%s) and %s", driveQuery, filter)
		}
	}
	if driveQuery == "" {
		driveQuery = "trashed = false"
	} else {
		driveQuery = fmt.Sprintf("(%s) and trashed = false", driveQuery)
	}

	call := clients.drive.Files.List().
		Q(driveQuery).
		PageSize(int64(maxResults)).
		Fields("nextPageToken, files(id,name,mimeType,modifiedTime,webViewLink,owners(emailAddress,displayName),driveId,parents,size)").
		OrderBy("modifiedTime desc")
	if pageToken != "" {
		call = call.PageToken(pageToken)
	}

	resp, err := call.Do()
	if err != nil {
		return mcp.ErrorResult(s.wrapGoogleError("Drive", err)), nil
	}
	files := make([]map[string]any, 0, len(resp.Files))
	for _, file := range resp.Files {
		files = append(files, simplifyDriveFile(file))
	}
	return mcp.JSONResult(map[string]any{
		"files":           files,
		"next_page_token": resp.NextPageToken,
		"query":           driveQuery,
	})
}

func (s *googleWorkspaceServer) handleDriveGetFile(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	v := validate.NewArgs(args)
	fileID := v.Required("file_id")
	if err := v.Validate(); err != nil {
		return mcp.ErrorResult(err), nil
	}

	ctx, cancel := context.WithTimeout(ctx, s.timeout)
	defer cancel()

	clients, _, err := s.newClients(ctx)
	if err != nil {
		return mcp.ErrorResult(err), nil
	}
	file, err := clients.drive.Files.Get(fileID).
		Fields("id,name,mimeType,modifiedTime,webViewLink,owners(emailAddress,displayName),driveId,parents,size").
		Do()
	if err != nil {
		return mcp.ErrorResult(s.wrapGoogleError("Drive", err)), nil
	}
	return mcp.JSONResult(simplifyDriveFile(file))
}

func simplifyDriveFile(file *drive.File) map[string]any {
	owners := make([]map[string]any, 0, len(file.Owners))
	for _, owner := range file.Owners {
		owners = append(owners, map[string]any{
			"email": owner.EmailAddress,
			"name":  owner.DisplayName,
		})
	}
	return map[string]any{
		"id":            file.Id,
		"name":          file.Name,
		"mime_type":     file.MimeType,
		"modified_time": file.ModifiedTime,
		"web_view_link": file.WebViewLink,
		"parents":       file.Parents,
		"size":          file.Size,
		"owners":        owners,
	}
}
