IMAGE := hopty-dev

.PHONY: dev test build down reset

dev:
	docker build --target dev -t $(IMAGE) -f Dockerfile.dev .
	docker run --rm -it -v "$(CURDIR):/src" -w /src $(IMAGE)

test:
	docker build --target test -f Dockerfile.dev .

build:
	docker build --target build -f Dockerfile.dev .

down:
	@true

reset:
	@true
