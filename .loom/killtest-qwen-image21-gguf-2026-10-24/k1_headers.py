"""K1: dump tensor names/shapes/types from partial GGUF and safetensors headers.

GGUF: parses the header, KV metadata and tensor-info table from a byte prefix
(no tensor data needed). safetensors: 8-byte little-endian length + JSON.

usage: k1_headers.py gguf <prefix-file> <out.json>
       k1_headers.py safetensors <prefix-file> <out.json>
"""

import json
import struct
import sys

GGML_TYPES = {
    0: "F32",
    1: "F16",
    2: "Q4_0",
    3: "Q4_1",
    6: "Q5_0",
    7: "Q5_1",
    8: "Q8_0",
    9: "Q8_1",
    10: "Q2_K",
    11: "Q3_K",
    12: "Q4_K",
    13: "Q5_K",
    14: "Q6_K",
    15: "Q8_K",
    16: "IQ2_XXS",
    17: "IQ2_XS",
    18: "IQ3_XXS",
    19: "IQ1_S",
    20: "IQ4_NL",
    21: "IQ3_S",
    22: "IQ2_S",
    23: "IQ4_XS",
    24: "I8",
    25: "I16",
    26: "I32",
    27: "I64",
    28: "F64",
    29: "IQ1_M",
    30: "BF16",
}
# GGUF metadata value types
SCALAR = {
    0: "<B",
    1: "<b",
    2: "<H",
    3: "<h",
    4: "<I",
    5: "<i",
    6: "<f",
    7: "<?",
    10: "<Q",
    11: "<q",
    12: "<d",
}


class Reader:
    def __init__(self, buf):
        self.buf = buf
        self.pos = 0

    def take(self, fmt):
        size = struct.calcsize(fmt)
        if self.pos + size > len(self.buf):
            raise EOFError(f"prefix too short at offset {self.pos}")
        (val,) = struct.unpack_from(fmt, self.buf, self.pos)
        self.pos += size
        return val

    def string(self):
        n = self.take("<Q")
        if self.pos + n > len(self.buf):
            raise EOFError(f"prefix too short at offset {self.pos}")
        s = self.buf[self.pos : self.pos + n].decode("utf-8", "replace")
        self.pos += n
        return s

    def value(self, vtype):
        if vtype in SCALAR:
            return self.take(SCALAR[vtype])
        if vtype == 8:
            return self.string()
        if vtype == 9:
            etype = self.take("<I")
            count = self.take("<Q")
            items = [self.value(etype) for _ in range(count)]
            return items if count <= 16 else f"<array of {count} type {etype}>"
        raise ValueError(f"unknown GGUF value type {vtype}")


def read_gguf(buf):
    r = Reader(buf)
    if r.buf[:4] != b"GGUF":
        raise ValueError("not a GGUF file")
    r.pos = 4
    version = r.take("<I")
    n_tensors = r.take("<Q")
    n_kv = r.take("<Q")
    kv = {}
    for _ in range(n_kv):
        key = r.string()
        kv[key] = r.value(r.take("<I"))
    tensors = {}
    for _ in range(n_tensors):
        name = r.string()
        n_dims = r.take("<I")
        # GGUF stores dims fastest-first; reverse to torch/numpy order
        dims = [r.take("<Q") for _ in range(n_dims)][::-1]
        ttype = r.take("<I")
        offset = r.take("<Q")
        tensors[name] = {
            "shape": dims,
            "dtype": GGML_TYPES.get(ttype, str(ttype)),
            "offset": offset,
        }
    return {
        "version": version,
        "n_tensors": n_tensors,
        "kv": kv,
        "tensor_info_end": r.pos,
        "tensors": tensors,
    }


def read_safetensors(buf):
    (n,) = struct.unpack_from("<Q", buf, 0)
    header = json.loads(buf[8 : 8 + n])
    meta = header.pop("__metadata__", {})
    tensors = {k: {"shape": v["shape"], "dtype": v["dtype"]} for k, v in header.items()}
    return {"n_tensors": len(tensors), "kv": meta, "tensors": tensors}


def main():
    kind, path, out = sys.argv[1:4]
    with open(path, "rb") as f:
        buf = f.read()
    info = read_gguf(buf) if kind == "gguf" else read_safetensors(buf)
    with open(out, "w") as f:
        json.dump(info, f, indent=1, default=str)
    print(f"{path}: {info['n_tensors']} tensors")
    for k, v in info["kv"].items():
        print(f"  kv {k} = {v}")
    if kind == "gguf":
        print(f"  tensor-info table ends at byte {info['tensor_info_end']:,}")


if __name__ == "__main__":
    main()
