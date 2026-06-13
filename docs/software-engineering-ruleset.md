# Software Engineering Ruleset

This document is the repository's broad software engineering decision
reference. Use it during implementation, refactoring, design review, and
rearchitecture work when choosing between paradigms, structures, patterns,
algorithms, and operating practices.

It is intentionally a ruleset, not an encyclopedia. Human software engineering
knowledge is too large and contextual to enumerate completely. This document
captures the durable decision rules that make those practices usable: when to
use a technique, when to avoid it, and which forces should dominate the choice.

## Prime Directive

Choose the simplest design that honestly represents the problem, protects real
invariants, can be tested, can be operated, and can be changed by the next
maintainer without reconstructing hidden context.

Prefer:

- explicit ownership over ambient authority
- clear contracts over clever coupling
- local reasoning over global knowledge
- boring solutions over fashionable ones
- measured complexity over speculative generality
- behavior-preserving increments over rewrites
- feedback from tests, types, logs, and users over preference

Reject designs that look simple only because they push complexity into callers,
operators, databases, queues, framework magic, or undocumented conventions.

## How To Use This Ruleset

For implementation tasks:

1. Identify the invariant, input boundary, output contract, and failure modes.
2. Locate the existing ownership boundary before adding a new one.
3. Prefer the local project pattern unless this document gives a concrete
   reason to change it.
4. Pick data structures from access patterns.
5. Pick algorithms from constraints, data volume, and correctness requirements.
6. Add tests that prove behavior and edge cases, not internal trivia.
7. Review the diff for duplicated knowledge, hidden state, unclear names,
   boundary leaks, and unhandled failure.

For rearchitecture tasks:

1. Name the current pain in operational terms: change amplification, defect
   risk, scale limit, unclear ownership, poor deployability, poor observability,
   security exposure, or domain mismatch.
2. Name the invariant that the new architecture must protect.
3. Separate essential complexity from accidental complexity.
4. Prefer a strangler, adapter, or parallel-run migration over a big-bang
   rewrite.
5. Preserve behavior while moving boundaries unless behavior change is the
   explicit goal.
6. Define rollback, compatibility, data migration, and observability before
   moving production traffic.
7. Stop when the architectural force is resolved; do not keep abstracting.

## Universal Engineering Principles

### Correctness

- Validate all external input at trust boundaries.
- Encode important invariants close to the code that owns them.
- Make impossible states unrepresentable when the language and cost allow it.
- Use assertions for programmer assumptions, validation for external input, and
  domain errors for expected business failures.
- Distinguish retryable, recoverable, permanent, and programmer errors.
- Preserve diagnostic context when wrapping or propagating errors.
- Never silently continue from corrupted or incoherent state.

### Simplicity

- Use the least powerful abstraction that solves the problem honestly.
- Keep functions cohesive and at one level of abstraction.
- Avoid long parameter lists, boolean mode flags, and generic "manager" objects.
- Prefer explicit control flow until repetition or domain variation proves a
  better model is needed.
- Remove dead code rather than preserving speculative options.
- Let duplication happen once when the abstraction is unclear; abstract when the
  shared concept is proven.

### Modularity

- Put each rule in one authoritative place.
- Separate policy from mechanism, orchestration from computation, and domain
  behavior from infrastructure.
- Hide volatile details behind stable local interfaces.
- Keep public APIs small, intention-revealing, and hard to misuse.
- Depend inward toward stable domain rules; adapt outward to frameworks,
  databases, APIs, queues, filesystems, clocks, and random sources.

### Changeability

- Favor designs where likely changes stay local.
- Use tests as the safety net for behavior-preserving changes.
- Keep migrations incremental and reversible where practical.
- Prefer additive compatibility before breaking contract changes.
- Document rationale when a decision would surprise a future maintainer.

### Operability

- Every production path needs timeouts, cancellation, useful errors, and
  observability proportional to risk.
- Make partial failure, retries, duplicate messages, and backpressure explicit.
- Prefer graceful degradation for non-critical dependencies.
- Treat logs, metrics, traces, alerts, and audit records as product behavior,
  not afterthoughts.

### Security

- Treat all external input, files, network responses, model output, and user
  content as untrusted.
- Use least privilege, defense in depth, secure defaults, and explicit allow
  lists at sensitive boundaries.
- Keep secrets out of source, logs, telemetry, prompts, and test fixtures.
- Prefer secure libraries and platform primitives over custom cryptography.
- Model abuse cases, not only happy-path authorization.

## Paradigm Rules

| Paradigm | Use When | Avoid When |
| --- | --- | --- |
| Procedural | The workflow is simple, linear, and action-oriented. | Rules and lifecycle state start scattering across routines. |
| Object-oriented | Identity, lifecycle, encapsulated state, and behavior-rich entities matter. | Objects become passive bags with all behavior in services. |
| Functional | You need deterministic transformations, composability, parsing, validation, concurrency safety, or testable pure logic. | It forces unnatural abstractions or unreadable cleverness for the team. |
| Declarative | Desired state matters more than step order: SQL, UI descriptions, infrastructure, policy, rules. | Debugging execution order or side effects is central. |
| Data-oriented | Performance depends on memory layout, batch processing, cache locality, or columnar operations. | Domain behavior and invariants are the main complexity. |
| Event-driven | Producers and consumers need temporal or ownership decoupling. | Immediate consistency and simple request/response semantics are required. |
| Reactive | Values change over time and the system must update live: UI, streams, sensors, dashboards. | The flow is simple enough for direct calls. |
| Actor model | Independent state machines communicate by messages and isolation is valuable. | You need simple local transactions or shared relational queries. |
| Logic/rule-based | Decisions are driven by many composable rules or inference. | Rules are few, stable, and clearer as ordinary code. |
| Concurrent/parallel | Work is independent and latency or throughput benefits justify coordination cost. | Shared mutable state and synchronization dominate the design. |
| Aspect-oriented | Cross-cutting concerns can be applied mechanically: tracing, metrics, policy. | It hides business behavior or makes control flow surprising. |
| Generic/metaprogramming | A type-level or code-generation abstraction removes real duplication and preserves clarity. | It makes errors obscure, build steps fragile, or readers dependent on magic. |

## Architecture Rules

### Default Architecture Choice

Start with a modular monolith unless concrete forces require distribution.
Distribution adds latency, partial failure, versioning, observability, security,
data ownership, and operational complexity. Use services only when independent
deployment, scaling, ownership, compliance, or failure isolation pays that cost.

### Common Styles

| Architecture | Use When | Avoid When |
| --- | --- | --- |
| Transaction script | Use cases are simple, mostly CRUD, and domain rules are thin. | Rules become duplicated across scripts. |
| Domain model | Business rules, invariants, lifecycle, and language distinctions are rich. | The domain is mostly data entry or admin CRUD. |
| Layered architecture | UI, application, domain, and infrastructure responsibilities are clear and stable. | Layers only pass calls through or hide coupling. |
| Hexagonal / ports and adapters | Core behavior must be isolated from frameworks, storage, APIs, queues, and tools. | The indirection does not protect any volatile boundary. |
| Clean / onion architecture | Business rules are long-lived and infrastructure will change. | It becomes ceremony around trivial behavior. |
| Modular monolith | One deployable unit can contain strong internal module boundaries. | Teams or scaling requirements demand independent runtime ownership. |
| Microservices | Independent deployability, ownership, scaling, fault isolation, or compliance is worth distributed-system cost. | The driver is fashion, team politics, or vague "scalability". |
| Service-oriented architecture | Enterprise capabilities integrate across multiple systems and governance boundaries. | A single product codebase can own the workflow more simply. |
| Event-driven architecture | Workflows are asynchronous, cross-boundary, and tolerant of eventual consistency. | Users require immediate end-to-end confirmation. |
| CQRS | Read and write shapes differ materially in scale, latency, or model. | It merely duplicates simple CRUD models. |
| Event sourcing | Audit, history, replay, temporal queries, or rebuilding projections are core requirements. | Current state is enough and event schema evolution would be costly. |
| Pipeline | Work is naturally staged: compilers, ETL, media, indexing, analysis. | Stages require constant cross-talk or global mutation. |
| Plugin architecture | Behavior must be extended independently by teams, users, or integrations. | Extension points are speculative. |
| Serverless | Event glue, spiky workloads, and low operational ownership dominate. | Cold starts, vendor constraints, or local workflows are critical. |
| Batch processing | Throughput matters more than immediate latency. | Users need interactive feedback. |
| Stream processing | Continuous data, windows, watermarks, and near-real-time detection matter. | Periodic batch jobs satisfy the business need. |
| Local-first | Offline work, sync, and user-owned local state are central. | Central authority and real-time consistency dominate. |
| Peer-to-peer | No central coordinator can be assumed or trusted. | A central authority would simplify correctness and operations. |

### Boundary Rules

- Put immediate invariants inside one transaction, aggregate, or process when
  possible.
- Use eventual consistency only when staleness, conflict handling, and retry
  semantics are explicit.
- Keep domain language inside bounded contexts; translate at context boundaries.
- Do not share databases across independently owned services unless the shared
  database is itself the product boundary and governance is explicit.
- Keep external protocols out of core domain types.
- Version contracts that cross process, team, customer, or storage boundaries.

## Domain Modeling Rules

Use richer domain modeling when:

- business terms have precise meanings
- state transitions have rules
- invalid combinations are expensive
- calculations or policies are reused
- auditability or explainability matters
- multiple teams use the same vocabulary differently

Prefer:

- entities for identity and lifecycle
- value objects for immutable concepts with equality by value
- aggregates for consistency boundaries
- repositories for collection-like persistence abstraction
- domain services for domain behavior that does not belong to one entity
- application services for orchestration of use cases
- domain events for facts that other parts of the system may react to
- anti-corruption layers when integrating with models that use different
  language or assumptions

Avoid:

- anemic domain models for complex domains
- rich domain models for trivial CRUD
- aggregates that span unrelated consistency needs
- leaking ORM, JSON, database, or API concerns into domain objects
- generic names that erase domain meaning

## Pattern Rules

Patterns are vocabulary for recurring forces. Do not introduce a pattern unless
it removes real complexity, clarifies ownership, isolates volatility, or
standardizes a repeated contract.

### Creational Patterns

| Pattern | Use When | Avoid When |
| --- | --- | --- |
| Factory function | Creation has a name, validation, defaults, or variant selection. | A direct constructor is clearer. |
| Factory method | Subclasses or implementations choose product creation. | Inheritance is not already justified. |
| Abstract factory | Families of related objects vary together. | Only one product varies. |
| Builder | Construction has many optional parts or required sequencing. | It only mirrors constructor fields. |
| Prototype | Cloning configured objects is clearer or cheaper than reconstructing. | Copy semantics are subtle or unsafe. |
| Singleton | A process truly has one resource identity and lifecycle. | It is just global state or hidden dependency. |
| Dependency injection | You need replaceable dependencies, test seams, or inversion of control. | It turns simple construction into configuration sprawl. |
| Service locator | Rarely; legacy or plugin runtime lookup where DI is impractical. | It hides dependencies from callers and tests. |

### Structural Patterns

| Pattern | Use When | Avoid When |
| --- | --- | --- |
| Adapter | External or legacy interface does not match local needs. | You can change the source interface safely. |
| Facade | A subsystem needs a simpler, stable entry point. | It becomes a god object. |
| Decorator | Responsibilities compose around one interface. | Order and interaction become hard to reason about. |
| Proxy | Access control, lazy loading, caching, remoting, or instrumentation is needed. | It obscures cost or failure modes. |
| Composite | Tree structures should be treated uniformly. | Parent and leaf behavior are fundamentally different. |
| Bridge | Abstraction and implementation must vary independently. | There is only one axis of variation. |
| Flyweight | Many small repeated objects need shared immutable state. | Object count is not a measured problem. |
| Module | Cohesive functions/data need a stable namespace and boundary. | The module becomes a junk drawer. |

### Behavioral Patterns

| Pattern | Use When | Avoid When |
| --- | --- | --- |
| Strategy | Algorithms vary behind one contract. | A simple conditional is clearer and stable. |
| State | Behavior varies by lifecycle state and transitions matter. | State can be represented by a small enum and simple checks. |
| Command | Actions need queueing, retry, undo, logging, permissions, or serialization. | Direct calls are enough. |
| Observer | Many consumers react to change without producer knowledge. | Ordering, error handling, or consistency must be tightly controlled. |
| Publish/subscribe | Producers and consumers need broader decoupling. | You need explicit request/response ownership. |
| Chain of responsibility | Multiple handlers may accept, reject, or transform a request. | Handler order is unclear or business-critical. |
| Template method | Algorithm skeleton is fixed and steps vary. | Composition would be clearer than inheritance. |
| Visitor | Many operations run over a stable object structure. | Object structure changes more often than operations. |
| Interpreter | You own a small language or expression grammar. | A full parser/library would be safer. |
| Mediator | Many components need coordinated interaction through one protocol. | It centralizes too much behavior. |
| Iterator | Traversal should not expose representation. | The structure is already simple and public by design. |
| Memento | State snapshots are needed for undo/restore. | Snapshots are too large, leaky, or inconsistent. |
| Specification | Business predicates need composition, reuse, and explanation. | One-off checks are clearer. |
| Null object | A neutral behavior object removes repetitive absence checks. | Absence must be explicit for correctness. |

### Enterprise And Integration Patterns

| Pattern | Use When | Avoid When |
| --- | --- | --- |
| Repository | Domain code needs collection-like persistence without storage details. | It only wraps an ORM with no semantic boundary. |
| Unit of work | Several changes must commit or roll back together. | The transaction is already obvious and local. |
| Data mapper | Domain objects should not know persistence schema. | Active record is simpler and domain is thin. |
| Active record | CRUD entities map directly to tables with little domain behavior. | Business rules are rich or persistence should be isolated. |
| Identity map | Repeated loads in a unit of work must preserve object identity. | Lifecycle and caching become obscure. |
| Lazy load | Data is expensive and often unused. | It hides I/O in domain logic or causes N+1 failures. |
| Outbox | A database write and message publish must not diverge. | Publishing is not coupled to committed state. |
| Inbox | Consumers must deduplicate messages. | Delivery is exactly-once by stronger infrastructure contract. |
| Saga | Long-running workflow crosses transactional boundaries. | One local transaction can protect the invariant. |
| Process manager | A workflow has state, timeouts, and reactions across events. | Simple event handlers are enough. |
| Materialized view | Read performance or shape differs from write model. | Fresh transactional reads are required and affordable. |
| API gateway | Many backend services need one client-facing entry point. | It becomes a business logic dumping ground. |
| Backend-for-frontend | Different clients need tailored APIs. | One client contract is enough. |
| Strangler fig | Replacing legacy behavior incrementally. | You can safely delete and rewrite without migration risk. |
| Anti-corruption layer | External model conflicts with local domain language. | The external model is already your real domain. |

### Reliability Patterns

| Pattern | Use When | Avoid When |
| --- | --- | --- |
| Timeout | Any remote, blocking, or user-visible operation. | Almost never avoid; choose deliberate defaults. |
| Retry with backoff | Failure is transient and the operation is idempotent or deduplicated. | Failure is permanent or retry amplifies overload. |
| Circuit breaker | A dependency can fail repeatedly and should be isolated. | Failure rate is low and local handling is enough. |
| Bulkhead | One resource pool failure must not exhaust the whole system. | Resource isolation adds more complexity than risk removed. |
| Rate limiter | Protect capacity, enforce quotas, or reduce abuse. | It hides root-cause capacity issues without feedback. |
| Load shedding | Serving some requests is better than total collapse. | Dropped work violates core correctness. |
| Idempotency key | Clients or queues may duplicate requests. | Duplicate work is impossible or harmless. |
| Dead-letter queue | Messages can fail permanently and need inspection. | Failed messages can be safely discarded with metrics. |
| Backpressure | Producers can overwhelm consumers. | Work volume is bounded and synchronous. |
| Graceful degradation | Optional features can fail without taking down core flows. | The degraded path would mislead users or corrupt data. |

## Data Structure Rules

Choose data structures from operations:

- lookup by key
- ordered iteration
- range query
- insertion/deletion position
- deduplication
- top-k/min/max
- prefix search
- graph traversal
- spatial query
- memory locality
- concurrency requirements
- serialization and persistence

| Data Structure | Use When | Avoid When |
| --- | --- | --- |
| Array / slice / vector | Fast indexed access, compact memory, sequential iteration. | Frequent middle inserts/deletes dominate. |
| Dynamic array | Size grows over time and amortized append is fine. | Stable references into storage are required. |
| Linked list | Frequent splicing with existing node references. | You need cache locality, indexing, or simple code. |
| Stack | Last-in-first-out work: parsing, undo, DFS, call simulation. | You need arbitrary access. |
| Queue | First-in-first-out work: scheduling, BFS, buffering. | Priority or random access matters. |
| Deque | Push/pop at both ends. | One-ended behavior is enough. |
| Hash map | Fast key-value lookup and ordering does not matter. | You need sorted order or stable iteration. |
| Hash set | Fast membership and deduplication. | You need counts or order. |
| Ordered map / balanced tree | Sorted keys, range queries, predecessor/successor. | Hash lookup is enough. |
| Binary search tree | Ordered dynamic set; use balanced variants in production. | Unbalanced input can destroy performance. |
| B-tree / B+tree | Disk/page-oriented indexes and databases. | Pure in-memory small collections. |
| LSM tree | Write-heavy storage with compaction tradeoffs. | Read amplification or compaction complexity is unacceptable. |
| Heap / priority queue | Repeated min/max extraction or scheduling. | You need arbitrary deletion without extra indexing. |
| Trie | Prefix lookup, autocomplete, routing, lexicons. | Memory overhead is too high for sparse data. |
| Suffix array/tree | Substring search over large text. | Simple search is fast enough. |
| Rope / piece table | Large editable text buffers. | Strings are small and immutable operations are cheap. |
| Graph adjacency list | Sparse graphs and traversal. | Dense graph edge checks dominate. |
| Graph adjacency matrix | Dense graphs or constant-time edge checks. | Graph is sparse. |
| Union-find | Dynamic connectivity and disjoint sets. | You need path details, not just component membership. |
| Bloom filter | Cheap probabilistic pre-check: maybe present, definitely absent. | False positives are unacceptable. |
| Count-min sketch | Approximate frequencies over streams. | Exact counts are required. |
| HyperLogLog | Approximate cardinality at scale. | Exact uniqueness counts are required. |
| Bitset / bitmap | Dense boolean flags and fast set operations. | Domain is sparse or unbounded. |
| Roaring bitmap | Compressed integer sets with fast operations. | Values are not integer-like or sets are tiny. |
| Ring buffer | Fixed-size streaming, logs, producer/consumer buffers. | Unbounded history is required. |
| Segment tree | Range queries with updates. | Data is static or queries are simple. |
| Fenwick tree | Prefix/range sums with point updates. | Operations are not associative sums-like. |
| Skip list | Ordered probabilistic structure with simple concurrency options. | Deterministic balancing is required. |
| KD-tree | Low-dimensional nearest-neighbor or spatial partitioning. | High-dimensional distance loses meaning. |
| R-tree | Spatial rectangles, geospatial indexes. | Data is not spatial or ranges are simple. |
| Inverted index | Text search across documents. | Exact key lookup is enough. |
| Columnar storage | Analytical scans over subsets of fields. | Transactional row updates dominate. |
| Row storage | Transactional reads/writes of whole records. | Analytics scans dominate. |
| CRDT | Concurrent offline/distributed edits must merge automatically. | A central transaction authority is acceptable. |
| Vector clock | Causality tracking in distributed systems. | Wall-clock ordering is enough and conflicts are simple. |

## Algorithm Rules

Select algorithms from correctness, input size, data distribution, latency,
memory, concurrency, and proof obligations.

### General Strategy

| Strategy | Use When | Avoid When |
| --- | --- | --- |
| Brute force | Inputs are tiny, clarity matters, or you need a correctness oracle. | Growth will make it unusable. |
| Divide and conquer | Problem splits into independent subproblems. | Combining results is more complex than solving directly. |
| Greedy | Local choices can be proven globally safe. | You cannot prove the greedy choice property. |
| Dynamic programming | Overlapping subproblems and optimal substructure exist. | State space explodes beyond constraints. |
| Backtracking | You need exhaustive constraint search with pruning. | Constraints are weak and search space is huge. |
| Branch and bound | Optimization search has strong bounds. | Bounds do not prune meaningfully. |
| Randomized | Average-case simplicity or probabilistic guarantees are acceptable. | Reproducibility or worst-case guarantees dominate. |
| Approximation | Exact solution is too expensive and near-optimal is useful. | Exactness is a correctness requirement. |
| Streaming | Data is too large or arrives continuously. | Full historical context is required. |
| Online | Decisions must be made before all input is known. | Offline optimization is possible and required. |
| Memoization | Repeated pure computations share arguments. | Inputs are mostly unique or memory is constrained. |
| Precomputation | Many queries reuse stable data. | Data changes frequently or startup cost is unacceptable. |

### Search And Graph Algorithms

| Algorithm | Use When |
| --- | --- |
| Linear search | Small or unsorted collections. |
| Binary search | Sorted data or monotonic predicate. |
| BFS | Shortest path in unweighted graph; level-order traversal. |
| DFS | Reachability, cycle detection, topological work, backtracking. |
| Topological sort | Dependency ordering in a DAG. |
| Dijkstra | Shortest path with non-negative edge weights. |
| Bellman-Ford | Shortest path with negative edges and cycle detection. |
| A* | Shortest path with an admissible useful heuristic. |
| Floyd-Warshall | All-pairs shortest paths on small or dense graphs. |
| Johnson | All-pairs shortest paths on sparse weighted graphs. |
| Kruskal / Prim | Minimum spanning tree. |
| Tarjan / Kosaraju | Strongly connected components. |
| Max flow / min cut | Capacity, matching, assignment, and network optimization. |
| Bipartite matching | Pairing two sets under constraints. |

### Sorting And Selection

| Algorithm | Use When |
| --- | --- |
| Quicksort | General in-memory sorting when average performance is fine. |
| Introsort | Production general-purpose sort with worst-case guard. |
| Merge sort | Stable sort, external sort, linked structures. |
| Heap sort | O(1) extra space and worst-case bound matter. |
| Insertion sort | Very small or nearly sorted data. |
| TimSort | Real-world partially ordered data; stable sorting. |
| Counting sort | Small bounded integer keys. |
| Radix sort | Large sets of fixed-width integer/string keys. |
| Quickselect | kth element or median without full sort. |
| External sort | Data exceeds memory. |

### String And Parsing Algorithms

| Algorithm / Technique | Use When |
| --- | --- |
| Regular expressions | Simple regular-language matching. |
| Parser combinators | Small grammars with composable parsing. |
| Recursive descent | Handwritten grammar with clear precedence/control. |
| Pratt parser | Expression parsing with precedence. |
| LL/LR parser generators | Formal grammar and robust tooling matter. |
| KMP | Repeated substring search with linear guarantee. |
| Boyer-Moore | Fast practical substring search on larger alphabets. |
| Aho-Corasick | Match many patterns simultaneously. |
| Levenshtein / edit distance | Fuzzy matching and correction. |
| Tokenization + AST | Semantics matter more than text shape. |

### Numerical And Optimization Algorithms

| Algorithm / Technique | Use When |
| --- | --- |
| Newton / gradient methods | Smooth optimization with derivatives or approximations. |
| Linear programming | Linear objective and constraints. |
| Integer programming | Discrete exact optimization justifies solver cost. |
| Simulated annealing | Approximate global optimization with rugged search space. |
| Genetic algorithms | Search space is poorly understood and fitness is cheap. |
| Kalman filter | Estimate hidden state from noisy measurements. |
| Monte Carlo | Sampling estimates are acceptable or exact integration is hard. |

### Distributed Algorithms

| Algorithm / Protocol | Use When |
| --- | --- |
| Leader election | One coordinator must own a role temporarily. |
| Two-phase commit | Atomic commit across participants is required and blocking is acceptable. |
| Saga | Distributed workflow can compensate instead of atomically commit. |
| Raft / Paxos | Consensus over replicated state is required. |
| Gossip | Eventually spread state in large decentralized systems. |
| Consistent hashing | Redistribute keys with minimal movement. |
| Quorum reads/writes | Tune consistency and availability in replicated storage. |
| CRDT merge | Independent replicas must converge without central coordination. |

## Database And Persistence Rules

| Need | Prefer |
| --- | --- |
| Strong relational integrity | Relational database with constraints and transactions. |
| Flexible document shape | Document store with explicit schema discipline. |
| Key lookup at high scale | Key-value store. |
| Graph relationships | Graph database or graph-shaped relational model. |
| Search | Search index/inverted index. |
| Analytics | Columnar store, warehouse, OLAP model. |
| Time series | Time-series database or partitioned append model. |
| Immutable audit | Append-only log or event store. |
| Offline sync | Local store plus conflict strategy. |

Rules:

- Put constraints in the database when they protect shared persisted truth.
- Keep business meaning in domain/application code unless the database is the
  authoritative boundary for that rule.
- Design migrations as deployable sequences: expand, backfill, dual read/write
  if needed, verify, contract.
- Index from query patterns, not guesses.
- Treat cache as derived data unless explicitly designed as source of truth.
- Define retention, deletion, privacy, and backup behavior early for sensitive
  data.

## API And Contract Rules

- Make contracts explicit: request shape, response shape, errors, idempotency,
  authorization, pagination, versioning, and compatibility.
- Prefer additive changes for public APIs.
- Use semantic status/errors that clients can act on.
- Separate transport DTOs from domain models when crossing trust or version
  boundaries.
- Validate at the edge and translate into internal types.
- Include correlation IDs or trace context across service boundaries.
- Document rate limits, retries, timeouts, and eventual consistency.

## Testing Rules

| Test Type | Use When |
| --- | --- |
| Unit tests | Pure logic, small contracts, edge cases. |
| Characterization tests | Legacy behavior must be preserved before refactor. |
| Integration tests | Boundaries with DB, filesystem, network, queues, or external APIs. |
| Contract tests | Provider/consumer compatibility matters. |
| End-to-end tests | Critical user workflows need confidence across layers. |
| Property-based tests | Many inputs should satisfy general invariants. |
| Fuzz tests | Parsers, decoders, protocol handling, security-sensitive input. |
| Golden tests | Structured output should remain stable and reviewable. |
| Snapshot tests | UI or large output changes need review, but avoid brittle snapshots. |
| Load tests | Throughput, latency, or resource limits matter. |
| Stress tests | Behavior under overload and resource exhaustion matters. |
| Soak tests | Leaks, drift, or long-running degradation are risks. |
| Chaos tests | Resilience under dependency or infrastructure failure matters. |
| Mutation tests | Test quality needs deeper signal. |
| Manual exploratory tests | UX, product fit, or ambiguous human behavior matters. |

Rules:

- Test behavior, contracts, and invariants before implementation details.
- Keep tests deterministic, isolated, readable, and self-checking.
- Put most coverage in fast tests; use fewer high-value slow tests.
- Add a regression test for each bug when feasible.
- Avoid absence-only assertions for behavior quality; assert positive evidence.
- Do not mock what you do not own unless isolation is necessary.
- Use fakes for local contracts, mocks for interaction assertions, and real
  dependencies for integration confidence.

## Refactoring And Legacy Rules

- Characterize current behavior before changing risky legacy code.
- Refactor in small behavior-preserving steps.
- Separate structural cleanup from behavior change when practical.
- Prefer extract, wrap, adapt, and strangle over big-bang replacement.
- Delete dead code after confirming no live path depends on it.
- Inline abstractions that no longer pay for themselves.
- Treat repeated local fixes in one area as evidence of a missing shared
  concept or contract.
- Use dependency seams to isolate hard-to-test code.
- Preserve public contracts until consumers are migrated.

## Rearchitecture Rules

Rearchitecture is justified when at least one of these forces is real and
measured or strongly evidenced:

- change amplification across unrelated areas
- unclear or conflicting ownership
- repeated defect class caused by boundary mismatch
- scale, latency, or reliability limit in the current shape
- security or compliance boundary is impossible to enforce
- deployment or operational coupling blocks delivery
- domain model no longer matches the business
- data ownership or consistency semantics are incoherent

Preferred migration patterns:

- strangler fig for legacy replacement
- branch by abstraction for changing implementations behind one contract
- expand-and-contract for schemas and APIs
- adapter/anti-corruption layer for incompatible models
- parallel run for high-risk behavior replacement
- dark launch for production signal without user exposure
- feature flags for controlled rollout and rollback
- shadow reads/writes only when privacy, cost, and correctness are understood

Do not rearchitect because:

- a pattern is fashionable
- code looks old but works and changes locally
- the team wants to avoid understanding the current system
- the new design has not named migration, rollback, and verification
- the proposed abstraction has no concrete volatility to hide

## Security And Privacy Rules

| Concern | Rule |
| --- | --- |
| Authentication | Prove identity with established protocols and secure session handling. |
| Authorization | Check permission at every sensitive boundary, not only in UI. |
| Input validation | Validate shape, size, type, encoding, and trust level. |
| Injection | Use parameterized APIs and structured builders. |
| XSS/HTML | Escape by context and avoid unsafe rendering. |
| CSRF | Use same-site cookies, tokens, and framework protections. |
| SSRF | Use allow lists, network egress controls, and URL/IP validation. |
| Secrets | Store in secret managers; never log or commit. |
| Cryptography | Use vetted libraries and modern protocols. |
| Supply chain | Pin, verify, scan, and update dependencies. |
| Privacy | Minimize collection, limit retention, redact logs, honor deletion. |
| Audit | Record who did what, when, and under which authority. |

## Performance Rules

- First make it correct and observable.
- Measure before optimizing unless the constraint is already known.
- Optimize the dominant cost, not the most visible code.
- Prefer algorithmic improvements before micro-optimizations.
- Preserve readability unless performance evidence justifies complexity.
- Define budgets for latency, memory, CPU, I/O, network, startup, and tail
  behavior.
- Watch p95/p99 latency, not only averages.
- Use caching only with explicit invalidation, staleness, and capacity policy.
- Use batching to amortize overhead when latency allows it.
- Use concurrency only when work is independent and coordination cost is lower
  than the benefit.

## Concurrency Rules

- Prefer immutability, ownership transfer, or message passing over shared
  mutable state.
- Keep locks small, ordered, and documented when ordering matters.
- Design cancellation, timeouts, and shutdown before launching background work.
- Make idempotency and deduplication explicit for retried work.
- Avoid fire-and-forget unless loss is acceptable and observable.
- Treat data races as correctness bugs, not performance tradeoffs.
- Test concurrency with stress, race detection, and failure injection where
  available.

## Observability And Operations Rules

- Logs should explain discrete events with enough context to diagnose.
- Metrics should track rates, errors, saturation, latency, queue depth, and
  business-relevant outcomes.
- Traces should cross process boundaries for user-visible workflows.
- Alerts should map to action and user impact.
- Dashboards should answer operational questions, not display every metric.
- Runbooks should include symptoms, diagnosis, mitigation, rollback, and
  escalation.
- Feature flags must have owners, expiry, and cleanup paths.
- Configuration must be typed, validated, and observable at startup.
- Release processes should be automated, repeatable, and reversible where
  practical.

## Frontend And UX Engineering Rules

- Model UI state explicitly: loading, empty, partial, error, stale, saving,
  disabled, optimistic, and permission-denied states.
- Keep domain rules out of presentation components unless the UI owns the rule.
- Prefer accessible semantic elements before custom controls.
- Validate at the client for feedback and at the server for authority.
- Avoid global mutable UI state unless the workflow truly spans components.
- Keep rendering deterministic from state.
- Optimize perceived performance with progressive loading, skeletons, and
  optimistic updates only when rollback is clear.
- Test critical flows with realistic data and viewport/accessibility concerns.

## AI-Assisted Engineering Rules

- Treat model output as untrusted until grounded in source, tests, logs, or
  cited documentation.
- Do not let generated summaries substitute for source-body evidence when code
  behavior matters.
- Keep prompts generic and contract-based; avoid scenario-specific steering in
  reusable instructions.
- Separate model strategy from harness or system invariants.
- Verify generated changes with the same tests and review standards as human
  changes.
- Preserve auditability: record important evidence paths, commands, and
  assumptions.

## Anti-Patterns

Avoid these unless a local design explicitly justifies them:

- god object, god service, or god module
- anemic domain model in a complex domain
- distributed monolith
- shared database across independent services without explicit governance
- global mutable state
- hidden I/O in pure-looking functions
- boolean mode flags
- stringly typed domain concepts
- magic numbers and unexplained sentinels
- premature abstraction
- speculative generality
- shotgun surgery
- copy/paste business rules
- cyclic dependencies
- leaky abstractions
- temporal coupling hidden in call order
- catch-all error handling
- swallowed exceptions
- unbounded queues
- retry storms
- cache without invalidation policy
- lock without timeout or ordering discipline
- test suite that requires order, wall-clock timing, or external luck
- mocks that encode implementation details instead of contracts
- framework-driven domain design
- migration without rollback
- observability after an incident instead of before rollout

## Final Selection Checklist

Before choosing or approving a paradigm, architecture, pattern, algorithm, or
data structure, answer:

1. What invariant does this protect?
2. What complexity does this remove?
3. What complexity does this add?
4. Who owns the data and rules?
5. What changes become easier?
6. What changes become harder?
7. What failure modes are introduced?
8. How will the design be tested?
9. How will the design be observed in production?
10. How can the design be migrated, rolled back, or simplified later?

If the answer is "because this is the standard pattern", the decision is not
yet justified. If the answer names a real force, an owned boundary, and a
verification path, the design is likely on solid ground.
