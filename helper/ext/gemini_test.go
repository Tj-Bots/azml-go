package ext

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestGeminiModelsAndRetired(t *testing.T) {
	var used []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/models" {
			fmt.Fprint(w, `{"models":[
				{"name":"models/gemini-2.0-flash","supportedGenerationMethods":["generateContent"]},
				{"name":"models/gemini-3.8-flash-lite","supportedGenerationMethods":["generateContent"]},
				{"name":"models/gemini-3.8-flash","supportedGenerationMethods":["generateContent"]},
				{"name":"models/gemini-4.0-flash-preview-05","supportedGenerationMethods":["generateContent"]},
				{"name":"models/gemini-3.8-flash-image","supportedGenerationMethods":["generateContent"]},
				{"name":"models/text-embedding-004","supportedGenerationMethods":["embedContent"]}]}`)
			return
		}
		model := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/models/"), ":generateContent")
		used = append(used, model)
		if model == "gemini-3.8-flash" { // its quota is over today
			w.WriteHeader(429)
			fmt.Fprint(w, `{"error":{"message":"Quota exceeded for requests per day"}}`)
			return
		}
		var body struct {
			Contents []struct {
				Parts []struct{ Text string } `json:"parts"`
			} `json:"contents"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		p := body.Contents[0].Parts[0].Text
		var lines []string
		_ = json.Unmarshal([]byte(p[strings.LastIndex(p, "\n\n")+2:]), &lines)
		out, _ := json.Marshal(lines)
		res, _ := json.Marshal(map[string]interface{}{"candidates": []interface{}{map[string]interface{}{"content": map[string]interface{}{"parts": []interface{}{map[string]string{"text": string(out)}}}}}})
		fmt.Fprint(w, string(res))
	}))
	defer srv.Close()
	old := GeminiBaseURL
	GeminiBaseURL = srv.URL
	defer func() { GeminiBaseURL = old }()

	got := geminiModelsFor(context.Background(), "K")
	if strings.Join(got, ",") != "gemini-3.8-flash,gemini-3.8-flash-lite,gemini-2.0-flash,gemini-4.0-flash-preview-05" {
		t.Fatalf("models %v", got)
	}
	res, err := GeminiTranslate(context.Background(), "K", []string{"a", "b"}, "Hebrew")
	if err != nil || len(res) != 2 || strings.Join(used, ",") != "gemini-3.8-flash,gemini-3.8-flash-lite" {
		t.Fatalf("%v %v used %v", res, err, used)
	}
}
