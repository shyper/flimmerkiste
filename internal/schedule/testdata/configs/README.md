Stored channel configurations as older versions wrote them (`kind` + `config`).
A live database keeps these after an update, so every version must read them:

- `TestStoredConfigs` (Go) parses each one, checks it is valid and compares the
  result with `../normalized/` (refresh with `go test ./internal/schedule -run StoredConfigs -update`).
- `web/src/tests/editor.test.js` opens the channel editor with both versions.

When the format changes, add a file in the old format here instead of editing existing ones.
