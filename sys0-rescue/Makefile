.PHONY: all clean small native run

ZIG ?= zig

# Smallest possible: musl static + ReleaseSmall + stripped.
small:
	$(ZIG) build-exe src/main.zig \
		-O ReleaseSmall \
		-target x86_64-linux-musl \
		-fstrip \
		--name sys0-rescue
	@ls -l sys0-rescue

native:
	$(ZIG) build-exe src/main.zig -O ReleaseSmall -fstrip --name sys0-rescue

# Cross-build the same matrix sys0 ships (linux/darwin/windows x amd64/arm64).
TARGETS = \
	x86_64-linux-musl \
	aarch64-linux-musl \
	x86_64-macos \
	aarch64-macos \
	x86_64-windows \
	aarch64-windows

all:
	@mkdir -p dist
	@for t in $(TARGETS); do \
		ext=""; case $$t in *windows*) ext=".exe";; esac; \
		out="dist/sys0-rescue_$${t}$${ext}"; \
		echo ">> $$out"; \
		$(ZIG) build-exe src/main.zig -O ReleaseSmall -target $$t -fstrip --name sys0-rescue \
			&& mv sys0-rescue$$ext "$$out" || exit 1; \
	done
	@ls -l dist

run: native
	./sys0-rescue --once

clean:
	rm -f sys0-rescue sys0-rescue.exe *.o
	rm -rf dist zig-cache .zig-cache
