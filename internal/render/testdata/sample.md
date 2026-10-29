# Heading One
## Heading Two
### Heading Three
#### Heading Four
##### Heading Five
###### Heading Six

Plain paragraph with **bold**, *italic*, ***both***, ~~strike~~, `inline code`, and a [link](https://example.com).
Literal tags must stay literal: [red]not red[-] and [::b]not bold[::-] and [#00ff00]no green[-].

> A blockquote line
> with **bold** inside

1. First numbered
2. Second numbered
   - nested bullet
     - deeper bullet
3. Third numbered

- [ ] open task
- [x] done task
* star bullet
+ plus bullet

---

| Name | Lang | Stars |
| ---- | :--: | ----: |
| coder | `go` | 42 |
| ergo | ts | 7 |

```go
package main

import "fmt"

// main prints a greeting.
func main() {
	msg := "hello [red]world"
	fmt.Println(msg, 42, true)
}
```

```json
{"name": "coder", "version": "0.9.188", "tags": ["cli", "tui"], "ok": true, "n": null}
```

```xml
<?xml version="1.0"?>
<config enabled="true"><item id="1">Hello &amp; bye</item><!-- note --></config>
```

```yaml
name: coder
version: 0.9.188
list:
  - a
  - b # comment
```

```python
def greet(name: str) -> str:
    """Docstring."""
    return f"hi {name}"  # comment
```

```bash
#!/usr/bin/env bash
for f in *.go; do echo "$f"; done
```

```sql
SELECT id, name FROM users WHERE active = 1 ORDER BY name;
```

```rust
fn main() { let x: i32 = 5; println!("{}", x); }
```

```ts
const add = (a: number, b: number): number => a + b; // sum
```

A very long line that should wrap sensibly at word boundaries rather than being chopped mid-word in the middle of the transcript pane because terminals are narrow sometimes and the canvas is narrower still.
