.PHONY: test demo-gates demo-gate-rerank

test:
	go test ./...

demo-gates:
	scripts/run_demo_gates.sh

demo-gate-rerank:
	MODES=rerank-merge scripts/run_demo_gates.sh
