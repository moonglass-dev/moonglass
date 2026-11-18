// Command server is the static HTTP server the deploy tests import as their
// test image (docs/specs/deploy-suite.md). It answers 200 on every path.
package main

import "net/http"

func main() {
	http.HandleFunc("/", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	if err := http.ListenAndServe(":8080", nil); err != nil {
		panic(err)
	}
}
