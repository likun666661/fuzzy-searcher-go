.PHONY: test service-run service-check service-local service-smoke demo-gates demo-gate-rerank

test:
	go test ./...

service-run:
	go run ./cmd/youtu-rag-service

service-check:
	go run ./cmd/youtu-rag-service --check-config

service-local:
	scripts/run_service_local.sh

service-smoke:
	scripts/run_service_smoke.sh

demo-gates:
	scripts/run_demo_gates.sh

demo-gate-rerank:
	MODES=rerank-merge scripts/run_demo_gates.sh
