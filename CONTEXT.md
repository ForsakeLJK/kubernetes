# Explicit NUMA Placement

Language for the research fork's explicit Pod NUMA placement feature.

## Language

**NUMA placement requirement**:
A Pod's hard requirement that its CPU and memory placement use a specified NUMA node. A Pod whose requirement cannot be satisfied must be refused admission.
_Avoid_: NUMA preference, best-effort placement

**NUMA node ID**:
The identifier of a NUMA node within the machine running the Pod. The same ID on different machines does not identify a shared location.

**Pod-wide placement**:
A single NUMA placement requirement shared by all application containers, init containers, and sidecars in a Pod.

**Memory-allocation confinement**:
Restricting a container's allowed memory-node mask to the requested NUMA node. This does not guarantee locality of every accessible shared page or previously allocated page.
