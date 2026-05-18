.PHONY: test service-run service-check service-local demo-gates demo-gate-rerank

test:
	go test ./...

service-run:
	go run ./cmd/youtu-rag-service

service-check:
	go run ./cmd/youtu-rag-service --check-config

service-local:
	scripts/run_service_local.sh

demo-gates:
	scripts/run_demo_gates.sh

demo-gate-rerank:
	MODES=rerank-merge scripts/run_demo_gates.sh
