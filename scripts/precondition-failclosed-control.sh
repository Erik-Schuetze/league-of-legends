#!/bin/sh
# scripts/precondition-failclosed-control.sh - the control for the class of
# defect that produced a green light wired to nothing.
#
#   sh scripts/precondition-failclosed-control.sh <command> <make target> [pass target]
#
# The defect this guards against: a gate whose precondition is absent printed a
# reason and exited 0, so the run reported success for a check that never
# happened. `make compliance-gnu` did exactly that - "skipped: docker is not
# installed" - while the CI step that runs it went green, which means the GNU
# userland half of the gate could disappear without anything turning red.
#
# The control does not read the target's source for the word "skip". It creates
# the missing precondition for real: it builds a PATH that contains every
# executable the current one has except the named command, checks that the
# command is really gone from it and that the tools the target needs are still
# there, and then runs the target under that PATH. The target has to fail, and
# its own output has to say which precondition was missing, so the failure is the
# guard reporting the missing tool rather than a crash somewhere else in the
# recipe. Then the same target is run again under the normal PATH, where it has
# to succeed: a rule that only ever fails is not a control either.
#
# It fails closed on its own preconditions. If the command is not installed at
# all, or the sanitised PATH cannot be built, or `make` is missing, there is
# nothing to prove and the control fails instead of reporting a pass.
#
# Needs sh, make, and the tools used below. No cluster, no network.

set -u

if [ "$#" -lt 2 ] || [ "$#" -gt 3 ]; then
	printf 'usage: %s <command> <make target> [pass target]\n' "$0" >&2
	exit 2
fi

ROOT=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
HIDE=$1
TARGET=$2
# The pass direction may name a cheaper target than the full gate: the point of
# it is that the same precondition, present, does not fail.
PASS_TARGET=${3:-$2}
WORK="$ROOT/.agent-artifacts/precondition-failclosed.$$"
SANDBIN="$WORK/path"

controls=0
broken=0
good() { controls=$((controls + 1)); printf 'PASS  %s\n' "$1"; }
bad() { broken=$((broken + 1)); printf 'FAIL  %s\n' "$1"; }
note() { printf '      %s\n' "$1"; }

case "$HIDE" in
*/*) printf 'FAIL  <%s> is a path; this control hides a name found on PATH\n' "$HIDE" >&2; exit 1 ;;
esac

for tool in make sh awk grep sed tail ls ln mkdir env; do
	if ! command -v "$tool" >/dev/null 2>&1; then
		printf 'FAIL  %s is not installed, so this control cannot create the condition it needs\n' "$tool"
		exit 1
	fi
done

REAL=$(command -v "$HIDE" 2>/dev/null) || REAL=''
if [ -z "$REAL" ]; then
	printf 'FAIL  %s is not installed, so there is no precondition to remove and this control would prove nothing.\n' "$HIDE"
	printf '      Install it, or stop running it as a gate.\n'
	exit 1
fi
note "$HIDE is at $REAL"

rm -rf "$WORK"
trap 'rm -rf "$WORK"' EXIT INT TERM
mkdir -p "$SANDBIN" || { printf 'FAIL  could not create %s\n' "$SANDBIN"; exit 1; }

# One directory holding a symlink for every executable name on the current PATH,
# except the one being hidden. The first name wins, which is the same rule the
# shell uses.
old_ifs=$IFS
IFS=:
for dir in $PATH; do
	IFS=$old_ifs
	[ -d "$dir" ] || continue
	for entry in "$dir"/*; do
		[ -e "$entry" ] || continue
		name=${entry##*/}
		[ "$name" = "$HIDE" ] && continue
		[ -x "$entry" ] || continue
		[ -e "$SANDBIN/$name" ] && continue
		ln -s "$entry" "$SANDBIN/$name" 2>/dev/null
	done
	IFS=:
done
IFS=$old_ifs

if env PATH="$SANDBIN" sh -c "command -v $HIDE" >/dev/null 2>&1; then
	printf 'FAIL  %s is still reachable through the sanitised PATH, so the control did not create its condition\n' "$HIDE"
	exit 1
fi
good "the sanitised PATH has no $HIDE (it has $(ls "$SANDBIN" | wc -l | tr -d ' ') other name(s) from the current PATH)"

# --- the failed direction ----------------------------------------------------
printf '\n-- %s is missing: make %s has to fail, and say why\n' "$HIDE" "$TARGET"
if env PATH="$SANDBIN" make -C "$ROOT" "$TARGET" >"$WORK/missing.log" 2>&1; then
	bad "make $TARGET exited 0 with $HIDE removed from PATH, so the run reports success for a check that did not happen"
	note "captured output: $WORK/missing.log"
	tail -5 "$WORK/missing.log" | while IFS= read -r line; do note "$line"; done
else
	rc=$?
	good "make $TARGET exits non-zero with $HIDE removed from PATH (exit $rc)"
fi
if grep -q "$HIDE" "$WORK/missing.log"; then
	good "the failure names $HIDE: $(grep -m1 "$HIDE" "$WORK/missing.log" | sed 's/^[[:space:]]*//')"
else
	bad "the failure does not mention $HIDE, so the target failed for some other reason and this proves nothing about the precondition"
	note "captured output: $WORK/missing.log"
	tail -5 "$WORK/missing.log" | while IFS= read -r line; do note "$line"; done
fi

# --- the direction that has to still work ------------------------------------
printf '\n-- %s is present: make %s has to succeed\n' "$HIDE" "$PASS_TARGET"
if make -C "$ROOT" "$PASS_TARGET" >"$WORK/present.log" 2>&1; then
	good "make $PASS_TARGET exits 0 with $HIDE on the normal PATH"
else
	bad "make $PASS_TARGET failed with $HIDE available, so the target is broken apart from this control"
	note "captured output: $WORK/present.log"
	tail -5 "$WORK/present.log" | while IFS= read -r line; do note "$line"; done
fi
note "last line with $HIDE present: $(tail -1 "$WORK/present.log")"

printf '\nprecondition-failclosed-control: %s control(s) held, %s broken\n' "$controls" "$broken"
[ "$broken" -eq 0 ] || exit 1
printf 'ok: make %s fails closed and loudly when %s is missing, and make %s succeeds when it is there\n' "$TARGET" "$HIDE" "$PASS_TARGET"
