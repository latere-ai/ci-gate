## What this changes

<!-- What is different for a repository that runs the bar: a gate's verdict, a command, a key of .lateregate.yaml, a message. -->

## Why

<!-- The problem this solves. Link the issue if there is one. -->

## Checklist

- [ ] `go test ./...` and `go tool lateregate` pass on your machine, and
      `npm run test:enums` if the change touches the TypeScript analyzer.
- [ ] A bug fix includes a test that fails without the fix.
- [ ] A new gate, or a change to what a gate asserts, starts from a spec
      in `specs/` and keeps the four principles in
      `specs/001-gate-principles.md`.
- [ ] A failure message says what to do next, for the person running
      the bar.
- [ ] `docs/` and `CHANGELOG.md` follow the change. A change that alters
      a gate's verdict on existing trees says what a repository has to do.
