package main

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

func main() {
	if len(os.Args) > 1 && (os.Args[1] == "db" || os.Args[1] == "admin") {
		return
	}
	if len(os.Args) > 1 && os.Args[1] == "enroll" {
		credentialPath := argument("--credential-file")
		if _, err := os.Stat(credentialPath); err == nil {
			return
		}
		if err := os.MkdirAll(filepath.Dir(credentialPath), 0o700); err != nil {
			panic(err)
		}
		if err := os.WriteFile(credentialPath, []byte("{\"credential\":\"fake-credential\",\"generation\":1}\n"), 0o600); err != nil {
			panic(err)
		}
		return
	}
	address := "0.0.0.0:8080"
	if value := os.Getenv("XISNOVE_UI_ADDR"); value != "" {
		address = value
	} else if value := os.Getenv("XISNOVE_AGENT_OBSERVABILITY_ADDRESS"); value != "" {
		address = value
	}
	handler := http.NewServeMux()
	for _, path := range []string{"/livez", "/readyz"} {
		handler.HandleFunc(path, func(writer http.ResponseWriter, _ *http.Request) { writer.WriteHeader(http.StatusNoContent) })
	}
	handler.HandleFunc("/metrics", func(writer http.ResponseWriter, _ *http.Request) {
		_, _ = writer.Write([]byte("xisnove_fake_ready 1\n"))
	})
	handler.HandleFunc("/", func(writer http.ResponseWriter, _ *http.Request) {
		_, _ = writer.Write([]byte(strings.TrimSpace(os.Getenv("XISNOVE_FAKE_VERSION"))))
	})
	if err := http.ListenAndServe(address, handler); err != nil {
		panic(err)
	}
}

func argument(name string) string {
	for index := 0; index+1 < len(os.Args); index++ {
		if os.Args[index] == name {
			return os.Args[index+1]
		}
	}
	panic("missing " + name)
}
