package main

import (
	"archive/zip"
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDeployNetlify(t *testing.T) {
	var files []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == "POST" && r.URL.Path == "/sites":
			_, _ = w.Write([]byte(`{"id":"site1"}`))
		case r.Method == "POST" && r.URL.Path == "/sites/site1/deploys":
			b, _ := io.ReadAll(r.Body)
			zr, _ := zip.NewReader(bytes.NewReader(b), int64(len(b)))
			for _, f := range zr.File {
				files = append(files, f.Name)
			}
			_, _ = w.Write([]byte(`{"id":"d1","state":"uploading"}`))
		case r.URL.Path == "/deploys/d1":
			_, _ = w.Write([]byte(`{"id":"d1","state":"ready","ssl_url":"https://glovebox-ab12.netlify.app"}`))
		default:
			w.WriteHeader(404)
		}
	}))
	defer srv.Close()
	netlifyAPI = srv.URL
	a := newLibApp(t)
	a.demo = false
	a.cfg.NetlifyToken = "tok"
	a.mu.Lock()
	p, _ := a.newProject("Glovebox", "", "")
	a.mu.Unlock()
	_ = os.MkdirAll(filepath.Join(p.Folder, "public", "img"), 0o755)
	_ = os.WriteFile(filepath.Join(p.Folder, "public", "index.html"), []byte("<h1>hi</h1>"), 0o644)
	_ = os.WriteFile(filepath.Join(p.Folder, "public", "img", "a.png"), []byte("png"), 0o644)
	_ = os.WriteFile(filepath.Join(p.Folder, "public", ".env"), []byte("SECRET=1"), 0o644)
	u, err := a.runDeploy(p, "", func(string) {})
	if err != nil || u != "https://glovebox-ab12.netlify.app" || p.SiteURL != u || p.NetlifySite != "site1" || len(p.Deploys) != 1 {
		t.Fatal(u, err, p.Deploys)
	}
	got := strings.Join(files, ",")
	if !strings.Contains(got, "index.html") || !strings.Contains(got, "img/a.png") || strings.Contains(got, ".env") {
		t.Fatal(got)
	}
}
