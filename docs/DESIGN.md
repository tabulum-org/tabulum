# Tabulum Design Document

The full design document is maintained separately and should be included
alongside this repository as the authoritative philosophical and architectural
reference.

See: `ai_ecosystem_design_doc_audited.md`

The design document covers:
- Vision and core principles
- The 8 kernel components (addressability, messaging, registry, shared state,
  agent interface, computation costs, internet access, infrastructure sandboxing)
- What is NOT in the kernel (emergent layer)
- Time and scale
- Open ecosystem and agent ingress
- Perturbation design (resolved: emergent only)
- Observation layer
- Persistence and recovery
- Funding and cost model
- Implementation approach
- Post-launch governance
- Related work and what makes this project different

All implementation decisions in this repository trace back to the design document.
When in doubt, the design document is the source of truth for *what* and *why*.
The code in this repository is the source of truth for *how*.
