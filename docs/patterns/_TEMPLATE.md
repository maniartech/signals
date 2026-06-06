<!--
  PATTERN TEMPLATE — copy this file verbatim to start a new pattern.
  Keep every section heading. Delete the HTML comments. Match the depth of the
  reference exemplar: docs/patterns/flow-control/load-shedding.md
  All code must use only the signatures in docs/patterns/README.md.
-->

# <Pattern Name>

**Family:** <Dispatch | Reliability | Flow-Control | Subscription Lifecycle | Architectural>
· **Also Known As:** <common aliases, or "—">
· **Status:** <✅ shipped | 🔜 v1.4 | mixed (note which parts)>

## Intent
<One or two sentences: what this pattern accomplishes and the problem it solves.
This is the elevator pitch — a reader should know if it's relevant from this alone.>

## Motivation
<A concrete, named scenario (a story with a domain: e-commerce, trading, telemetry).
Show the naive approach first and make its problem *visceral* — a real failure,
outage, or bug. Then introduce the pattern as the resolution. Include short code.>

## Applicability
**Use this pattern when:**
- <bullet>
- <bullet>

**Avoid it (or prefer another pattern) when:**
- <bullet> → see <Related Pattern>

## Structure
<A text/ASCII diagram showing the participants and the flow of an emission through
them. Sequence-style ("Emitter → Signal → Listener₁ … Listenerₙ") is encouraged.>

## Participants
<A table of the roles involved and each one's responsibility.>

| Participant | Responsibility |
|-------------|----------------|
| <role>      | <what it does> |

## Collaborations
<Step-by-step description of how the participants interact at runtime — the dynamic
behavior, not just the static roles.>

## Consequences
**Benefits**
- ✓ <benefit>

**Liabilities**
- ✗ <cost / risk / trade-off>

<Where relevant, state explicitly which corner of the "bounded memory / never-wait /
never-lose" trilemma this pattern sacrifices.>

## Implementation
<Numbered, deep notes for someone building on this: concurrency considerations,
ordering, context handling, gotchas, honest caveats, performance characteristics.
This is the architect-grade section.>

1. <note>
2. <note>

## Sample Code
<A complete, realistic, copy-pasteable example (looks compilable) with commentary.
Prefer a believable domain over toy `foo`/`bar`. Show the *right* usage; optionally
contrast with a wrong usage and its symptom.>

```go
// ...
```

## Variations
<Common variants and when to choose each.>

## Known Uses
<Real-world analogues and prior art: TCP, Kafka, LMAX Disruptor, Reactive Streams,
the Go stdlib, etc. Anchor the pattern in established practice.>

## Related Patterns
<Cross-links to other catalog patterns: which combine with this, which are
alternatives, which are prerequisites.>
- **[<Pattern>](<path>)** — <relationship>
