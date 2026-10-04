# Design: Replace survey with huh for TUI forms

**Date:** 2026-04-19  
**Status:** Approved

## Goal

Replace `github.com/AlecAivazis/survey/v2` with `github.com/charmbracelet/huh` in the `commit` package to improve TUI aesthetics while preserving full JSON config-driven flexibility.

## Constraints

- Public API of `FillOutForm() ([]byte, error)` must not change.
- The `.git-zf.json` config schema (field types, options, template) must remain fully intact.
- Users can still add, remove, and reorder form fields via config.
- All fields are shown at once (huh's native grouped UX).

## Architecture

The change is contained entirely within the `commit` package (`form.go`). No other packages (`cmd`, `git`) are touched.

### `loadForm()` signature change

**Before:**
```go
func loadForm() ([]*survey.Question, string, error)
```

**After:**
```go
func loadForm() (*huh.Form, func() map[string]interface{}, string, error)
```

The second return value is a closure that, after `form.Run()`, extracts answers from the bound variables and returns `map[string]interface{}` for template rendering.

### `FillOutForm()` change

```go
form, extract, tmpl, err := loadForm()
if err != nil { ... }
if err := form.Run(); err != nil { return nil, err }
answers := extract()
// assembleMessage unchanged
```

## `loadForm()` Internals

A `[]string` slice of length `len(items)` holds one bound variable per field. The loop maps `FormItem.Form` to a `huh` field:

| `form` value | huh field | notes |
|---|---|---|
| `"select"` | `huh.NewSelect[string]()` | `huh.NewOption(opt.Desc, opt.Name)` — display is `Desc`, committed value is `Name` (e.g. `"feat"`) |
| `"input"` | `huh.NewInput()` | |
| `"multiline"` | `huh.NewText()` | |

Required validation is applied via `.Validate(func(s string) error { ... })` on the field.

All fields are placed in a single `huh.NewGroup(fields...)`, then `huh.NewForm(group)`.

### Extractor closure

```go
func() map[string]interface{} {
    m := make(map[string]interface{}, len(items))
    for i, item := range items {
        m[item.Name] = strings.TrimSpace(values[i])
    }
    return m
}
```

## `assembleMessage()` simplification

The `survey.OptionAnswer` type assertion is removed — values are already plain strings after extraction. String trimming stays.

## Dependencies

| Action | Package |
|---|---|
| Add | `github.com/charmbracelet/huh` |
| Remove | `github.com/AlecAivazis/survey/v2` |

All other dependencies (`cobra`, `viper`, `mapstructure`) are unchanged.
