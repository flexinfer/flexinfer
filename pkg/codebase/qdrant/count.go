package qdrant

import (
	"context"
	"errors"
	"fmt"
	"net/http"
)

func (c *Client) Count(ctx context.Context, filter map[string]any) (int, error) {
	path := fmt.Sprintf("/collections/%s/points/count", c.collection)
	body := map[string]any{
		"exact":  true,
		"filter": filter,
	}

	var resp struct {
		Result struct {
			Count int `json:"count"`
		} `json:"result"`
	}

	if err := c.doJSON(ctx, http.MethodPost, path, body, &resp); err != nil {
		if errors.Is(err, ErrCollectionNotFound) {
			return 0, nil
		}
		return 0, err
	}
	return resp.Result.Count, nil
}
