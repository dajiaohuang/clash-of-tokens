package businessweb

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"clash-of-tokens/internal/browserbidi"
)

// aiStudioBuildBiDi uses the selected iframe's browsing context, including a
// cross-origin SafeContentFrame. The shell is never used to send Gemini calls.
func (c *Client) aiStudioBuildBiDi(parent context.Context, appURL, path string, body []byte) (aiStudioBuildFetchResult, error) {
	ctx, cancel := context.WithTimeout(parent, aiStudioBuildMaxRun)
	defer cancel()
	client, err := browserbidi.Connect(ctx, c.browser.CDPURL)
	if err != nil {
		return aiStudioBuildFetchResult{}, err
	}
	defer client.Close()
	tab, err := client.NewTab(ctx)
	if err != nil {
		return aiStudioBuildFetchResult{}, err
	}
	if err = client.Navigate(ctx, tab, appURL); err != nil {
		return aiStudioBuildFetchResult{}, err
	}
	for i := 0; i < 30; i++ {
		var clicked string
		if err = client.EvaluateJSON(ctx, tab, aiStudioBuildClickScript, &clicked); err != nil {
			return aiStudioBuildFetchResult{}, err
		}
		if clicked == "" {
			break
		}
		select {
		case <-ctx.Done():
			return aiStudioBuildFetchResult{}, ctx.Err()
		case <-time.After(time.Second):
		}
	}
	selector, _ := json.Marshal(aiStudioBuildPreviewSelector)
	frame := ""
	for i := 0; i < 30 && frame == ""; i++ {
		// BiDi window RemoteValues expose context IDs without reading any
		// cross-origin DOM or returning token-bearing page contents.
		var selected struct {
			Type   string `json:"type"`
			Result struct {
				Value []struct {
					Type  string `json:"type"`
					Value struct {
						Context string `json:"context"`
					} `json:"value"`
				} `json:"value"`
			} `json:"result"`
		}
		err = client.Call(ctx, "script.evaluate", map[string]any{"expression": "Array.from(document.querySelectorAll(" + string(selector) + ")).map(e=>e.contentWindow)", "target": map[string]string{"context": tab}, "awaitPromise": false, "resultOwnership": "none", "serializationOptions": map[string]int{"maxObjectDepth": 2}}, &selected)
		if err != nil {
			return aiStudioBuildFetchResult{}, err
		}
		for _, window := range selected.Result.Value {
			if window.Type != "window" || window.Value.Context == "" {
				continue
			}
			var tree struct {
				Contexts []struct {
					URL string `json:"url"`
				} `json:"contexts"`
			}
			if client.Call(ctx, "browsingContext.getTree", map[string]any{"root": window.Value.Context, "maxDepth": 0}, &tree) == nil && len(tree.Contexts) == 1 && aiStudioBuildPreviewURLAllowed(tree.Contexts[0].URL) {
				frame = window.Value.Context
				break
			}
		}
		if frame == "" {
			select {
			case <-ctx.Done():
				return aiStudioBuildFetchResult{}, ctx.Err()
			case <-time.After(time.Second):
			}
		}
	}
	if frame == "" {
		return aiStudioBuildFetchResult{}, errors.New("AI Studio Build preview frame has no trusted loaded context")
	}
	script := aiStudioBuildFetchScript(randomID("build-"), path, body)
	for {
		var raw json.RawMessage
		if err = client.EvaluateJSON(ctx, frame, script, &raw); err != nil {
			return aiStudioBuildFetchResult{}, err
		}
		if string(raw) != "false" {
			var result aiStudioBuildFetchResult
			if json.Unmarshal(raw, &result) != nil {
				return result, errors.New("invalid AI Studio Build result")
			}
			return result, nil
		}
		select {
		case <-ctx.Done():
			return aiStudioBuildFetchResult{}, ctx.Err()
		case <-time.After(100 * time.Millisecond):
		}
	}
}
