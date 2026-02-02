# Open Questions

## Data Model
- [ ] **Data Structuring**: How should the `Key -> Value` mapping be structured?
    *   *Options*: Flat KV, hierarchical keys (filesystem-like), or composite keys (Row + Col + TS as in Bigtable).
- [ ] **Value Data Types**: What data types should be allowed for the `Value`?
    *   *Options*:
        *   Raw Bytes (application handles serialization/schema).
        *   Structured Document (JSON/BSON, allows server-side indexing of fields).
        *   Primitive Types (String, Int, List, Map - Redis style).
- [ ] **Schema Enforcement**: Is SofaDB schema-less (schemaless JSON) or does it enforce column families strictly?
- [ ] **Query Language**: Will there be a query language (SQL-like) or just Key-Value API (`Put`, `Get`, `Scan`)?

## Storage Engine
- [ ] **Storage Engine**: Should the LSM-tree indices be purely in-memory (à la Bitcask) or strictly disk-based with caching (à la Bigtable/LevelDB)?
    *   *Context*: The `research_compilation.md` suggests LSM-tree, but the specific memory/disk split needs deciding.

## Implementation
- [ ] **Consensus Library**: Should we implement Raft from scratch (for learning/control) or use a production-grade library like `hashicorp/raft`?
    *   *Context*: Building from scratch is high effort but matches the "from scratch" ethos if that's the goal.
- [ ] **API Interface**: What will the client-facing API look like?
    *   *Options*: gRPC, REST/HTTP, Redis-compatible, Custom Protocol.

## Operational
- [ ] **Deployment**: Are we targeting Kubernetes, bare metal, or just local development for now?
