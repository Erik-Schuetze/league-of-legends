# Regenerates the checked-in Data Dragon projection.
#
# The site reads web/src/data/*.json for game-data identity, so that a build
# needs no network access:
#
#   champions.json   id, name, slug, roles, Data Dragon version, CDN icon path
#   items.json       "<numeric id>": { name, icon } for every item
#   runes.json       "<numeric id>": { name, icon } for every rune
#   spells.json      "<numeric id>": { name, icon } for every summoner spell
#
# All four come out of one run of one script, because a page mixes them: a
# champion page names a champion and draws the items, runes and spells of a
# build. Generating them from different releases would make the icons describe
# a different patch from the names.
#
# They are generated, reviewed and committed. This makefile is the way to
# regenerate them, because the root Makefile is not this directory's to change.
#
#   make -f web/scripts/champion-data.mk
#   make -f web/scripts/champion-data.mk DD_VERSION=16.18.1
#
# DD_VERSION pins the Data Dragon release. Left empty, the newest published
# release is used, and the checked-in files then describe a version the
# aggregate tree may not have static data for yet.

DD_VERSION ?=
SCRIPTS_DIR := $(dir $(lastword $(MAKEFILE_LIST)))
WEB_DIR := $(SCRIPTS_DIR)..
DATA_DIR := $(WEB_DIR)/src/data
DD_ARGS := $(if $(DD_VERSION),--version $(DD_VERSION),)
SCRATCH := $(WEB_DIR)/.ddragon-check

.PHONY: champions items runes spells champions-check

# Everything, which is also what `npm run data:champions` runs.
champions:
	node $(SCRIPTS_DIR)fetch-ddragon.mjs $(DD_ARGS)

# One kind at a time, for the rare case where only one file is wrong.
items:
	node $(SCRIPTS_DIR)fetch-ddragon.mjs $(DD_ARGS) --only items

runes:
	node $(SCRIPTS_DIR)fetch-ddragon.mjs $(DD_ARGS) --only runes

spells:
	node $(SCRIPTS_DIR)fetch-ddragon.mjs $(DD_ARGS) --only spells

# Regenerates into a scratch directory inside web/ and diffs every file, so a
# reader can tell whether a Data Dragon release changed the projection before
# committing it.
champions-check:
	rm -rf $(SCRATCH)
	node $(SCRIPTS_DIR)fetch-ddragon.mjs $(DD_ARGS) --out-dir $(SCRATCH)
	diff -u $(DATA_DIR)/champions.json $(SCRATCH)/champions.json
	diff -u $(DATA_DIR)/items.json $(SCRATCH)/items.json
	diff -u $(DATA_DIR)/runes.json $(SCRATCH)/runes.json
	diff -u $(DATA_DIR)/spells.json $(SCRATCH)/spells.json
	rm -rf $(SCRATCH)
	@echo "web/src/data/*.json matches Data Dragon"
