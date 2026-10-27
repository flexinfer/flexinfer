"""K2: compare two k2_load_forward.py output tensors made from identical seeded inputs.

Both runs start from the same seeded noise, so raw cosines include a shared-noise
term. The noise-orthogonal cosines project that out and measure model agreement.

usage: k2_compare.py <a.pt> <b.pt>
"""

import sys

import torch

SEED = 1234  # must match k2_load_forward.py


def cosine(a, b):
    a, b = a.float().flatten(), b.float().flatten()
    return float(torch.dot(a, b) / (a.norm() * b.norm()))


def orthogonal_to(x, n):
    x, n = x.float().flatten(), n.float().flatten()
    return x - (x @ n) / (n @ n) * n


a = torch.load(sys.argv[1])
b = torch.load(sys.argv[2])
noise = torch.randn(1, 256, 64, generator=torch.Generator().manual_seed(SEED))
for key in ("v1", "x0_hat", "final"):
    print(
        f"{key:7s} cos={cosine(a[key], b[key]):.4f}"
        f"  noise-orthogonal cos={cosine(orthogonal_to(a[key], noise), orthogonal_to(b[key], noise)):.4f}"
        f"  cos(a,noise)={cosine(a[key], noise):.3f} cos(b,noise)={cosine(b[key], noise):.3f}"
    )
# a seeded-noise baseline: two unrelated N(0,1) tensors of the same shape
g = torch.Generator().manual_seed(0)
r1, r2 = torch.randn(a["v1"].shape, generator=g), torch.randn(
    a["v1"].shape, generator=g
)
print(f"unrelated-noise baseline cos={cosine(r1, r2):.4f}")
