.PHONY: test service-run demo-gates demo-gate-rerank

test:
	go test ./...

service-run:
	go run ./cmd/youtu-rag-service

demo-gates:
	scripts/run_demo_gates.sh

demo-gate-rerank:
	MODES=rerank-merge scripts/run_demo_gates.sh
