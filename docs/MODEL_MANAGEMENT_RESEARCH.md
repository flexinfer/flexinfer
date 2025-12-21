# Model Management & Caching Architectures for Kubernetes

## Research Summary: Current Approaches

### 1. The "Naive" Approach (Current FlexInfer state)

- **Mechanism:** Each Deployment creates a distinct PVC (ReadWriteOnce). An Init Container or the serving container itself downloads the model at startup.
- **Pros:** Simple, practically zero isolation issues.
- **Cons:**
  - **Storage Waste:** 10 deployments of "Llama3" = 10 copies of weights (100GB+ waste).
  - **Slow Startup (Cold Start):** Every new deployment (even if just moving nodes) triggers a full download if the volume doesn't travel with it effectively.
  - **No Sharing:** Unable to scale replicas across nodes without re-downloading.

### 2. KServe / ModelMesh

- **Mechanism:** Uses a "Storage Initializer" init container to pull models from S3/GCS/PVC. KServe v0.11+ introduces `ClusterStorageContainer` for broader access.
- **Caching:** Supports `LocalModelCache` (via `hostPath` mounts) to leverage node-local storage speed.
- **Pros:** Industry standard, comprehensive.
- **Cons:** Heavy operational burden (Istio, Knative dependencies often required). Complexity is often overkill for smaller (K3s) clusters.

### 3. Fluid (Data Orchestration)

- **Mechanism:** Abstraction layer that caches data (models) closer to compute. Uses engines like Alluxio or JindoFS to turn S3 object storage into a POSIX-compliant distributed cache.
- **Pros:** Extremely efficient for massive scale; decouples storage from compute entirely.
- **Cons:** Very complex to setup and maintain in a lightweight K3s environment.

### 4. Shared Filesystems (NFS / Longhorn RWX)

- **Mechanism:** A single ReadWriteMany (RWX) PVC is mounted by all pods.
- **Pros:** "Write once, read everywhere." Simplifies model updates.
- **Cons & Gotchas:**
  - **Throughput:** If 10 GPUs try to load a 20GB model simultaneously from NFS, the network saturates.
  - **Locking & Corruption:**
    - _Multi-Attach Errors:_ Longhorn can get stuck with stale volume attachments (especially after node failures), leaving pods in `ContainerCreating` indefinitely until manual intervention (clearing `pendingNodeID`).
    - _Stale File Handles:_ NFS servers restarting or network blips can cause "Stale file handle" errors on clients, requiring pod restarts.
    - _SQLite Risks:_ Never put SQLite databases (e.g., for model metadata or vector stores) on NFS/RWX. SQLite relies on file locking that is reliably broken on network filesystems, leading to "database is locked" errors or corruption.

---

## FlexInfer Opportunity: "Smart" ModelCache

We can differentiate `flexinfer` by building a lightweight, "batteries-included" model management system that feels native to K3s but offers the sophistication of KServe.

### Concept: The `ModelCache` CRD

Instead of embedding model details (backend, model name) solely in the `ModelDeployment`, we extract the _Model Artifact_ into its own resource.

```yaml
apiVersion: ai.flexinfer/v1alpha1
kind: ModelCache
metadata:
  name: llama3-8b
spec:
  source:
    huggingface: "meta-llama/Meta-Llama-3-8B"
  storage:
    strategy: "Auto" # Options: LocalPath, SharedPVC, Ephemeral
    retention: "Keep"
```

### Key Differentiators & Features

#### 1. Tiered Storage Strategies

We can support multiple strategies suited for K3s:

- **`NodeLocal` (High Performance & Resilience):**
  - **How:** A DaemonSet or Job pre-downloads the model to a hostPath (e.g., `/var/lib/flexinfer/models/llama3`) on GPU nodes.
  - **Benefit:** Native disk performance (NVMe speed). Zero network overhead during inference loading. **Immune to NFS/Longhorn networking issues.**
- **`SharedPVC` (Ease of Use - with Caveats):**
  - **How:** A single RWX PVC is created and managed by the controller.
  - **Benefit:** Immediate availability on any node. Good for smaller models or high-bandwidth networks.
  - **Safety:** The `ModelCache` controller should enforce `ReadOnlyMany` mounting for the inference pods to prevent accidental corruption or locking issues. Only the "Downloader Job" gets Write access.

#### 2. Pre-Flight Caching (Eliminating Cold Starts)

With a `ModelCache` resource, we can "Warm" the cache before a deployment even starts.

- **Current:** `kubectl apply model-deploy.yaml` -> Pod Creating -> Downloading (10 mins) -> Ready.
- **Proposed:** `ModelCache` becomes `Ready` -> `ModelDeployment` proceeds. Rolling updates can wait for the _new_ model to be fully cached before terminating the old pods.

#### 3. Intelligent PVC Management

- **Deduplication:** The controller detects if "llama3" is already needed by another deployment and reuses the existing PVC/Local Path.
- **Garbage Collection:** Reference counting on `ModelCache`. If no deployments use "llama2" anymore, the cache is evicted (deleted) to free up expensive NVMe space.
- **Corrupt-safe:** Centralized downloader (Singleton Job) ensures only one process writes to the cache at a time, preventing corruption.

### Recommended Architecture for Implementation

**Phase 1: Shared Storage (The MVP)**

1.  Status: **Proposed**
2.  Modify `ModelDeployment` controller to check for a common "Model Store" PVC before creating a new one.
3.  If the model exists in the shared store, mount it **ReadOnly (`readOnly: true`)** to avoid file locking issues.
4.  If not, run a _single_ Job to download it to the shared store, then mount.

**Phase 2: The `ModelCache` CRD (The Differentiator)**

1.  Status: **Future**
2.  Implement the CRD to explicit manage the lifecycle.
3.  Add `NodeLocal` strategy using a DaemonSet to sync models to `/var/lib/flexinfer/cache` on all nodes. This is the **preferred strategy** for production AI to avoid NFS drawbacks.

### Challenges & Mitigation

- **RWX on K3s:** `local-path` (default in K3s) does NOT support RWX. We must rely on the user having Longhorn or NFS for the "SharedPVC" strategy.
  - _Mitigation:_ Default to "NodeLocal" or "PrivatePVC" (current behavior) if no RWX storage class is detected.
- **Cache Invalidation:** How do we know if the model on disk is corrupt?
  - _Mitigation:_ Store checksum/hash in the `ModelCache` status. Verify before use.
- **Stale Attachments:** Longhorn multi-attach errors.
  - _Mitigation:_ The `ModelCache` controller should favor `ReadOnlyMany` mounts for consumers. This often bypasses some of the stricter locking/consistency checks that cause writer-conflicts.
