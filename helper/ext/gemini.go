package ext

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// Gemini (the user's own free API key, /us → Universal → Gemini Key): AI translation of subtitles.
// Like gemini-srt-translator: the lines go in batches as a JSON array, the answer is a JSON array
// of the same length, so every line keeps its place and timing.

var GeminiBaseURL = "https://generativelanguage.googleapis.com/v1beta" // a var for the tests

// geminiModels: tried in order (a model the key can't use falls to the next one).
var geminiModels = []string{"gemini-2.5-flash", "gemini-2.0-flash"}

var geminiHTTP = &http.Client{Timeout: 3 * time.Minute}

func geminiDo(ctx context.Context, key, method, path string, body []byte) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, method, GeminiBaseURL+path, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("x-goog-api-key", strings.TrimSpace(key))
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := geminiHTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 20<<20))
	if resp.StatusCode != http.StatusOK {
		var e struct {
			Error struct {
				Message string `json:"message"`
			} `json:"error"`
		}
		if json.Unmarshal(b, &e) == nil && e.Error.Message != "" {
			return nil, fmt.Errorf("Gemini: %s", e.Error.Message)
		}
		return nil, fmt.Errorf("Gemini: HTTP %d", resp.StatusCode)
	}
	return b, nil
}

// GeminiCheckKey: the key works (lists the models).
func GeminiCheckKey(ctx context.Context, key string) error {
	if len(strings.TrimSpace(key)) < 20 || strings.ContainsAny(strings.TrimSpace(key), " \n") {
		return fmt.Errorf("this is not a Gemini API key")
	}
	_, err := geminiDo(ctx, key, "GET", "/models?pageSize=1", nil)
	return err
}

// langNameFor: "Hebrew" for "he" (the prompt is clearer with a name).
func langNameFor(code string) string {
	if n, ok := tmdbLangNames[strings.ToLower(code)]; ok {
		return n
	}
	return code
}

// GeminiTranslate: the lines translated to lang, in the same order (batches of 150).
func GeminiTranslate(ctx context.Context, key string, lines []string, lang string) ([]string, error) {
	out := make([]string, 0, len(lines))
	const batch = 150
	for start := 0; start < len(lines); start += batch {
		end := start + batch
		if end > len(lines) {
			end = len(lines)
		}
		part, err := geminiBatch(ctx, key, lines[start:end], lang)
		if err != nil {
			return nil, err
		}
		out = append(out, part...)
	}
	return out, nil
}

func geminiBatch(ctx context.Context, key string, lines []string, lang string) ([]string, error) {
	in, _ := json.Marshal(lines)
	prompt := "You translate movie and TV subtitles into " + langNameFor(lang) + ".\n" +
		"Input: a JSON array of subtitle lines in order. Output: ONLY a JSON array of strings with " +
		fmt.Sprint(len(lines)) + " items, the translation of each line at the same index.\n" +
		"Keep the meaning, tone and names; use natural spoken language and the right gender. Keep line breaks (\\n), " +
		"HTML tags like <i> and symbols like ♪ as they are. Do not merge, split, skip or explain lines.\n\n" + string(in)
	reqBody, _ := json.Marshal(map[string]interface{}{
		"contents": []interface{}{map[string]interface{}{"role": "user", "parts": []interface{}{map[string]string{"text": prompt}}}},
		"generationConfig": map[string]interface{}{
			"temperature":      0.2,
			"responseMimeType": "application/json",
		},
		"safetySettings": []interface{}{ // subtitles have violence and swearing: do not block them
			map[string]string{"category": "HARM_CATEGORY_HARASSMENT", "threshold": "BLOCK_NONE"},
			map[string]string{"category": "HARM_CATEGORY_HATE_SPEECH", "threshold": "BLOCK_NONE"},
			map[string]string{"category": "HARM_CATEGORY_SEXUALLY_EXPLICIT", "threshold": "BLOCK_NONE"},
			map[string]string{"category": "HARM_CATEGORY_DANGEROUS_CONTENT", "threshold": "BLOCK_NONE"},
		},
	})
	var lastErr error
	for _, model := range geminiModels {
		for attempt := 0; attempt < 2; attempt++ {
			b, err := geminiDo(ctx, key, "POST", "/models/"+model+":generateContent", reqBody)
			if err != nil {
				lastErr = err
				low := strings.ToLower(err.Error())
				if strings.Contains(low, "not found") || strings.Contains(low, "not supported") {
					break // this model: next one
				}
				if strings.Contains(low, "quota") || strings.Contains(low, "rate") || strings.Contains(low, "exhausted") {
					select {
					case <-ctx.Done():
						return nil, ctx.Err()
					case <-time.After(20 * time.Second):
					}
				}
				continue
			}
			var r struct {
				Candidates []struct {
					Content struct {
						Parts []struct {
							Text string `json:"text"`
						} `json:"parts"`
					} `json:"content"`
				} `json:"candidates"`
			}
			if json.Unmarshal(b, &r) != nil || len(r.Candidates) == 0 || len(r.Candidates[0].Content.Parts) == 0 {
				lastErr = fmt.Errorf("Gemini: empty answer")
				continue
			}
			txt := strings.TrimSpace(r.Candidates[0].Content.Parts[0].Text)
			txt = strings.TrimSuffix(strings.TrimPrefix(strings.TrimPrefix(txt, "```json"), "```"), "```")
			var out []string
			if err := json.Unmarshal([]byte(strings.TrimSpace(txt)), &out); err != nil || len(out) != len(lines) {
				lastErr = fmt.Errorf("Gemini: the answer did not match the lines")
				continue
			}
			return out, nil
		}
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("Gemini: no answer")
	}
	return nil, lastErr
}
