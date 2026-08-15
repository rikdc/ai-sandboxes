# Issue #42 transport spike results

msb version: msb 0.6.8
date: 2026-08-15T00:36:18Z

| Probe                                    | Res  | Notes
|------------------------------------------|------|------
| A: --port H:G, guest bind 127.0.0.1      | FAIL | host could not reach guest-loopback (expected — confirms #42 comment)
| B: --port H:G, guest bind 0.0.0.0        | PASS | confirms --port targets guest network interface
| C: msb ssh serve + ssh -L                | FAIL | ssh -L exited 255 — see /tmp/spike42-ssh.log and /tmp/spike42-serve.log
