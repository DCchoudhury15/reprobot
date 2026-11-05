#!/bin/sh
# Rebuilds the captured sanitizer outputs in out/ from the programs in src/.
#
# Each case is compiled and run in a fresh gcc:15 container with no network.
# Only the program's stderr is saved, as out/<name>.txt. Addresses and pids
# change on every run, so a rerun gives a diff even when nothing else changed.
#
# Usage: sh testdata/sanitizer/regen.sh
set -eu

here=$(cd "$(dirname "$0")" && pwd)
fixture=$(cd "$here/../fixtures/tinyparse" && pwd)
image=${IMAGE:-gcc:15}

asan="-fsanitize=address -g -O1 -fno-omit-frame-pointer"
asan0="-fsanitize=address -g -O0 -fno-omit-frame-pointer"
ubsan="-fsanitize=undefined -fno-sanitize-recover=all -g -O1"
asan_env="ASAN_OPTIONS=detect_leaks=0"
ubsan_stack="UBSAN_OPTIONS=print_stacktrace=1"
ubsan_nostack="UBSAN_OPTIONS=print_stacktrace=0"

mkdir -p "$here/out"

# capture NAME COMPILER SOURCE FLAGS ENV [ARGS...]
# SOURCE is a path inside the container: /src/... or /fixture/...
capture() {
	name=$1 cc=$2 src=$3 flags=$4 env=$5
	shift 5
	set +e
	docker run --rm --network none --cap-drop ALL \
		--security-opt no-new-privileges \
		-v "$here/src:/src:ro" -v "$fixture:/fixture:ro" \
		-e "$env" "$image" \
		sh -c '
			cc=$1 flags=$2 src=$3
			shift 3
			cd /tmp
			$cc $flags -o /tmp/prog "$src" >/tmp/cc.log 2>&1 || { cat /tmp/cc.log >&2; exit 99; }
			exec /tmp/prog "$@" >/dev/null
		' sh "$cc" "$flags" "$src" "$@" 2>"$here/out/$name.txt"
	rc=$?
	set -e
	if [ "$rc" -eq 99 ] || [ "$rc" -eq 125 ]; then
		echo "$name: compile or docker error" >&2
		cat "$here/out/$name.txt" >&2
		exit 1
	fi
	echo "$name: exit $rc"
}

capture heap_overflow_read gcc /src/heap_overflow_read.c "$asan" "$asan_env"
capture heap_overflow_read_O0 gcc /src/heap_overflow_read.c "$asan0" "$asan_env"
capture heap_overflow_read_shifted gcc /src/heap_overflow_read_shifted.c "$asan" "$asan_env"
capture heap_overflow_write gcc /src/heap_overflow_write.c "$asan" "$asan_env"
capture stack_overflow gcc /src/stack_overflow.c "$asan" "$asan_env"
capture global_overflow gcc /src/global_overflow.c "$asan" "$asan_env"
capture use_after_free gcc /src/use_after_free.c "$asan" "$asan_env"
capture double_free gcc /src/double_free.c "$asan" "$asan_env"
capture null_deref gcc /src/null_deref.c "$asan" "$asan_env"
capture cpp_overflow g++ /src/cpp_overflow.cc "$asan" "$asan_env"
capture deep_stack gcc /src/deep_stack.c "$asan" "$asan_env"
capture bad_free gcc /src/bad_free.c "$asan" "$asan_env"
capture alloc_too_big gcc /src/alloc_too_big.c "$asan" "$asan_env"
capture no_bug gcc /src/no_bug.c "$asan" "$asan_env"

capture ubsan_overflow gcc /src/ubsan_overflow.c "$ubsan" "$ubsan_nostack"
capture ubsan_overflow_stack gcc /src/ubsan_overflow.c "$ubsan" "$ubsan_stack"
for mode in shift null member index misaligned div; do
	capture "ubsan_$mode" gcc /src/ubsan_kinds.c "$ubsan" "$ubsan_stack" "$mode"
done

capture tinyparse gcc /fixture/vuln/tinyparse.c "$asan" "$asan_env" \
	/fixture/repro/input.bin
