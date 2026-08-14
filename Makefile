.PHONY: backend frontend build run dev clean

backend:
	cd backend && go build -o english-reading .

frontend:
	cd frontend && npm install && npm run build

build: backend frontend

run: build
	cd backend && ./english-reading

dev:
	@echo "Run backend:  cd backend && go run ."
	@echo "Run frontend: cd frontend && npm run dev"

clean:
	rm -rf backend/english-reading backend/data frontend/dist frontend/node_modules
