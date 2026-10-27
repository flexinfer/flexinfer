#!/usr/bin/env python3
"""CPU-only tests for build/server-diffusers.py: the pipeline call contract and
quantized component loading (Qwen-Image 2.1, slices R2 and R3 of
.loom/10-research-qwen-image-21-uncensored-2026-10-24.md).

The server imports torch, diffusers, fastapi, pydantic and PIL at module scope.
Each one that is not installed is replaced by a minimal stub before the server
loads, so the contract tests run on a bare python:3.12-slim (the CI job). The
RealLibraries tests need torch, gguf and diffusers >= 0.41 and skip without
them; they pin the upstream facts the contract relies on.

Run: python3 scripts/test_server_diffusers.py
"""

import asyncio
import contextlib
import functools
import importlib
import importlib.util
import inspect
import io
import os
import sys
import tempfile
import types
import unittest
from pathlib import Path
from unittest import mock

SERVER = Path(__file__).resolve().parent.parent / "build" / "server-diffusers.py"
STUBBED: set[str] = set()


def _install(name: str, module: types.ModuleType) -> None:
    sys.modules[name] = module
    parent, _, child = name.rpartition(".")
    if parent:
        setattr(sys.modules[parent], child, module)
    STUBBED.add(name)


def _missing(name: str) -> bool:
    try:
        importlib.import_module(name)
        return False
    except ImportError:
        return True


class _StubCallable:
    """Stands in for any class or function the server imports from a stub."""

    def __init__(self, *args, **kwargs):
        self.__dict__.update(kwargs)
        self.args = args
        self.kwargs = kwargs


def _stub_module(name: str, **attrs) -> types.ModuleType:
    module = types.ModuleType(name)
    module.__dict__.update(attrs)
    cache: dict[str, type] = {}

    def __getattr__(attr):
        if attr.startswith("__"):
            raise AttributeError(attr)
        if attr not in cache:
            cache[attr] = type(attr, (_StubCallable,), {})
        return cache[attr]

    module.__getattr__ = __getattr__
    return module


def _stub_torch() -> None:
    class Module:
        def parameters(self):
            return iter(())

        def to(self, *args, **kwargs):
            return self

    class Parameter:
        def __init__(self, data=None, requires_grad=True):
            self.data = data
            self.requires_grad = requires_grad

    class Generator:
        def __init__(self, device=None):
            self.device = device

        def manual_seed(self, seed):
            self.seed = seed
            return self

    nn = _stub_module(
        "torch.nn",
        Module=Module,
        Parameter=Parameter,
        RMSNorm=type("RMSNorm", (Module,), {}),
    )
    torch = _stub_module(
        "torch",
        __version__="2.9.0",
        float32="float32",
        float16="float16",
        bfloat16="bfloat16",
        Tensor=type("Tensor", (), {}),
        Generator=Generator,
        inference_mode=contextlib.nullcontext,
        version=types.SimpleNamespace(hip=None, cuda=None),
        cuda=types.SimpleNamespace(
            is_available=lambda: False, empty_cache=lambda: None
        ),
    )
    _install("torch", torch)
    _install("torch.nn", nn)
    _install("torch.nn.functional", _stub_module("torch.nn.functional"))


def _stub_pil() -> None:
    class FakeImage:
        def __init__(self, mode="RGB", size=(64, 64)):
            self.mode = mode
            self.size = size
            self.width, self.height = size

        def convert(self, mode):
            return FakeImage(mode, self.size)

        def resize(self, size, *args):
            return FakeImage(self.mode, size)

        def save(self, buffer, format=None):
            buffer.write(b"\x89PNG stub")

    image = _stub_module(
        "PIL.Image",
        Image=FakeImage,
        LANCZOS=1,
        NEAREST=0,
        new=lambda mode, size, color=None: FakeImage(mode, size),
        open=lambda fp: FakeImage(),
    )
    _install("PIL", _stub_module("PIL"))
    _install("PIL.Image", image)


def _stub_fastapi() -> None:
    class HTTPException(Exception):
        def __init__(self, status_code, detail=None):
            super().__init__(detail)
            self.status_code = status_code
            self.detail = detail

    class FastAPI:
        def __init__(self, *args, **kwargs):
            pass

        def _route(self, *args, **kwargs):
            return lambda fn: fn

        get = post = _route

    fastapi = _stub_module(
        "fastapi",
        FastAPI=FastAPI,
        HTTPException=HTTPException,
        File=lambda default=None, **kw: default,
        Form=lambda default=None, **kw: default,
    )
    _install("fastapi", fastapi)
    _install("fastapi.responses", _stub_module("fastapi.responses"))


def _stub_pydantic() -> None:
    class BaseModel:
        def __init__(self, **values):
            for cls in reversed(type(self).__mro__):
                for name in getattr(cls, "__annotations__", {}):
                    if hasattr(cls, name):
                        setattr(self, name, getattr(cls, name))
            for name, value in values.items():
                setattr(self, name, value)

    _install(
        "pydantic",
        _stub_module(
            "pydantic", BaseModel=BaseModel, Field=lambda default=None, **kw: default
        ),
    )


for _name, _stub in (
    ("torch", _stub_torch),
    ("PIL", _stub_pil),
    ("fastapi", _stub_fastapi),
    ("pydantic", _stub_pydantic),
):
    if _missing(_name):
        _stub()
if "torch" in STUBBED or _missing("diffusers"):
    # Real diffusers needs real torch; never mix a real diffusers with stub torch.
    for _name in [
        n for n in sys.modules if n == "diffusers" or n.startswith("diffusers.")
    ]:
        del sys.modules[_name]
    _install("diffusers", _stub_module("diffusers", __version__="0.0-stub"))
    _install("diffusers.quantizers", _stub_module("diffusers.quantizers"))

_spec = importlib.util.spec_from_file_location("server_diffusers", SERVER)
server = importlib.util.module_from_spec(_spec)
with contextlib.redirect_stdout(io.StringIO()):
    _spec.loader.exec_module(server)

REAL_DIFFUSERS = "diffusers" not in STUBBED and "torch" not in STUBBED


def _real_qwen_image21():
    try:
        from diffusers import QwenImage21Pipeline

        return QwenImage21Pipeline
    except ImportError:
        return None


# --- fake pipelines ---------------------------------------------------------


class FakeOutput:
    def __init__(self):
        self.images = [server.Image.new("RGB", (64, 64))]


def recording(call):
    """Record the kwargs a pipeline receives; unexpected ones raise TypeError
    exactly as the real __call__ would. The signature stays inspectable."""

    @functools.wraps(call)
    def wrapper(self, **kwargs):
        call(self, **kwargs)
        self.calls.append(kwargs)
        return FakeOutput()

    return wrapper


class _FakePipeline:
    def __init__(self):
        self.calls = []
        self.scheduler = object()


class QwenImage21Pipeline(_FakePipeline):
    """__call__ signature of diffusers 0.41.0 QwenImage21Pipeline."""

    @recording
    def __call__(
        self,
        prompt=None,
        image=None,
        negative_prompt=None,
        true_cfg_scale=1.0,
        height=None,
        width=None,
        num_inference_steps=40,
        sigmas=None,
        num_images_per_prompt=1,
        generator=None,
        latents=None,
        prompt_embeds=None,
        prompt_embeds_mask=None,
        negative_prompt_embeds=None,
        negative_prompt_embeds_mask=None,
        output_type="pil",
        return_dict=True,
        attention_kwargs=None,
        callback_on_step_end=None,
        callback_on_step_end_tensor_inputs=("latents",),
        output_resolution=1024,
        use_kv_cache=True,
    ):
        pass


class QwenImageEditPlusPipeline(_FakePipeline):
    """Qwen-Image v1 shape: true CFG plus a distilled guidance_scale."""

    @recording
    def __call__(
        self,
        prompt=None,
        image=None,
        negative_prompt=None,
        true_cfg_scale=4.0,
        height=None,
        width=None,
        num_inference_steps=50,
        guidance_scale=None,
        generator=None,
    ):
        pass


class StableDiffusionXLPipeline(_FakePipeline):
    """SDXL takes **kwargs, so the filter must pass everything through."""

    @recording
    def __call__(
        self,
        prompt=None,
        height=None,
        width=None,
        num_inference_steps=50,
        guidance_scale=5.0,
        negative_prompt=None,
        generator=None,
        **kwargs,
    ):
        pass


class StrictPipeline(_FakePipeline):
    """A pipeline without **kwargs and without negative_prompt."""

    @recording
    def __call__(self, prompt=None, num_inference_steps=20, guidance_scale=3.5):
        pass


class FakeUpload:
    def __init__(self, data: bytes):
        self.data = data

    async def read(self):
        return self.data


def png_bytes() -> bytes:
    buffer = io.BytesIO()
    server.Image.new("RGB", (8, 8)).save(buffer, format="PNG")
    return buffer.getvalue()


def edit_request(**values):
    """Call /v1/images/edits with every form field explicit (None by default),
    so a real FastAPI install never leaks its Form()/File() markers in."""
    params = inspect.signature(server.edit_images).parameters
    kwargs = {name: None for name in params}
    kwargs.update(n=1, response_format="b64_json")
    kwargs.update(values)
    return server.edit_images(**kwargs)


SERVER_ENV = (
    "MODEL_FAMILY",
    "MODEL_ID",
    "MODEL",
    "DEFAULT_GUIDANCE_SCALE",
    "DEFAULT_TRUE_CFG_SCALE",
    "DEFAULT_NUM_INFERENCE_STEPS",
    "DEFAULT_NEGATIVE_PROMPT",
    "QUANTIZATION",
    "TRANSFORMER_QUANTIZATION",
    "TEXT_ENCODER_QUANTIZATION",
    "TRANSFORMER_FILE",
    "TRANSFORMER_PATH",
    "TRANSFORMER_REPO",
    "TRANSFORMER_REVISION",
    "TRANSFORMER_CLASS",
    "BNB_COMPUTE_DTYPE",
    "SKIP_WARMUP",
    "WARMUP_RESOLUTIONS",
)


class ServerTestCase(unittest.TestCase):
    def setUp(self):
        self._env = mock.patch.dict(os.environ)
        self._env.start()
        for name in SERVER_ENV:
            os.environ.pop(name, None)
        server._call_parameters_cache.clear()
        server._logged_dropped_kwargs.clear()
        server.pipeline = None
        server.current_model = None
        server.current_model_family = None
        server.PIPELINE_MODE = "text2image"

    def tearDown(self):
        self._env.stop()
        server.pipeline = None
        server.current_model = None
        server.current_model_family = None

    def quiet(self, fn, *args, **kwargs):
        with contextlib.redirect_stdout(io.StringIO()):
            return fn(*args, **kwargs)

    def serve(self, pipe, model_id):
        os.environ["MODEL_ID"] = model_id
        server.pipeline = pipe
        server.current_model = model_id
        return pipe


# --- R2: model families -----------------------------------------------------


class ModelFamilyTest(ServerTestCase):
    def test_normalize_accepts_qwen_image_spellings(self):
        for value, want in (
            ("qwenimage21", "qwenimage21"),
            ("Qwen-Image-2.1", "qwenimage21"),
            ("qwen_image21", "qwenimage21"),
            ("qwenimage", "qwenimage"),
            ("qwen-image", "qwenimage"),
            ("sdxl-turbo", "sdxl"),
            ("sd", None),
        ):
            with self.subTest(value=value):
                self.assertEqual(server._normalize_model_family(value), want)

    def test_class_names_route_qwen_image(self):
        self.assertEqual(
            server._class_name_model_family("QwenImage21Pipeline"), "qwenimage21"
        )
        self.assertEqual(
            server._class_name_model_family("QwenImageEditPlusPipeline"), "qwenimage"
        )
        self.assertEqual(server._class_name_model_family("FluxPipeline"), "flux")

    def test_repo_ids_route_qwen_image(self):
        for model_id, want in (
            ("Qwen/Qwen-Image-2.1", "qwenimage21"),
            ("Qwen/Qwen-Image-2.1-Turbo", "qwenimage21"),
            ("abenzerps/Qwen-Image-2.1-Uncensored-Quantized", "qwenimage21"),
            ("Qwen/Qwen-Image-Edit-2509", "qwenimage"),
            ("stabilityai/sdxl-turbo", "sdxl"),
            ("black-forest-labs/FLUX.1-dev", "flux"),
        ):
            with self.subTest(model_id=model_id):
                self.assertEqual(server._heuristic_model_family(model_id), want)

    def test_qwen_image_defaults(self):
        self.assertEqual(server._default_steps("Qwen/Qwen-Image-2.1"), 40)
        self.assertEqual(server._default_steps("Qwen/Qwen-Image-2.1-Turbo"), 8)
        self.assertEqual(server._default_guidance_scale("Qwen/Qwen-Image-2.1"), 1.0)
        self.assertEqual(server._default_guidance_scale("Qwen/Qwen-Image"), 4.0)
        os.environ["DEFAULT_GUIDANCE_SCALE"] = "2.5"
        self.assertEqual(server._default_guidance_scale("Qwen/Qwen-Image-2.1"), 2.5)
        os.environ["DEFAULT_TRUE_CFG_SCALE"] = "3.0"
        self.assertEqual(server._default_guidance_scale("Qwen/Qwen-Image-2.1"), 3.0)
        # trueCfgScale only speaks for true-CFG families.
        self.assertEqual(
            server._default_guidance_scale("stabilityai/stable-diffusion-xl-base-1.0"),
            2.5,
        )


# --- R2: call contract ------------------------------------------------------


class CallContractTest(ServerTestCase):
    def test_filter_drops_kwargs_the_signature_lacks(self):
        pipe = QwenImage21Pipeline()
        kwargs = {"prompt": "p", "guidance_scale": 7.5, "num_inference_steps": 4}
        out = self.quiet(server._filter_call_kwargs, pipe, kwargs)
        self.assertEqual(out, {"prompt": "p", "num_inference_steps": 4})

    def test_filter_keeps_everything_for_var_kwargs(self):
        pipe = StableDiffusionXLPipeline()
        kwargs = {"prompt": "p", "guidance_scale": 7.5, "anything": 1}
        self.assertEqual(server._filter_call_kwargs(pipe, kwargs), kwargs)

    def test_unreadable_signature_passes_kwargs_through(self):
        pipe = StrictPipeline()
        with mock.patch.object(server.inspect, "signature", side_effect=ValueError):
            self.assertEqual(
                server._filter_call_kwargs(pipe, {"x": 1}),
                {"x": 1},
            )

    def test_qwen_image21_takes_guidance_as_true_cfg(self):
        pipe = QwenImage21Pipeline()
        out = self.quiet(
            server._adapt_call_kwargs,
            pipe,
            {"prompt": "p", "guidance_scale": 4.0, "negative_prompt": None},
            "Qwen/Qwen-Image-2.1",
        )
        self.assertNotIn("guidance_scale", out)
        self.assertEqual(out["true_cfg_scale"], 4.0)
        # CFG runs only with a negative prompt, so a blank one is supplied.
        self.assertEqual(out["negative_prompt"], " ")

    def test_qwen_image21_default_runs_without_cfg(self):
        pipe = QwenImage21Pipeline()
        out = self.quiet(
            server._adapt_call_kwargs,
            pipe,
            {"prompt": "p", "negative_prompt": "blurry"},
            "Qwen/Qwen-Image-2.1",
        )
        self.assertEqual(out["true_cfg_scale"], 1.0)
        self.assertNotIn("negative_prompt", out)

    def test_explicit_true_cfg_and_negative_prompt_win(self):
        pipe = QwenImage21Pipeline()
        out = self.quiet(
            server._adapt_call_kwargs,
            pipe,
            {
                "prompt": "p",
                "guidance_scale": 7.0,
                "true_cfg_scale": 3.0,
                "negative_prompt": "blurry",
            },
            "Qwen/Qwen-Image-2.1",
        )
        self.assertEqual(out["true_cfg_scale"], 3.0)
        self.assertEqual(out["negative_prompt"], "blurry")

    def test_qwen_image_v1_does_not_get_distilled_guidance(self):
        pipe = QwenImageEditPlusPipeline()
        out = self.quiet(
            server._adapt_call_kwargs,
            pipe,
            {"prompt": "p", "guidance_scale": 5.0},
            "Qwen/Qwen-Image-Edit-2509",
        )
        self.assertNotIn("guidance_scale", out)
        self.assertEqual(out["true_cfg_scale"], 5.0)

    def test_sdxl_kwargs_are_unchanged(self):
        pipe = StableDiffusionXLPipeline()
        kwargs = {
            "prompt": "p",
            "guidance_scale": 7.5,
            "negative_prompt": "blurry",
            "num_inference_steps": 20,
            "width": 1024,
            "height": 1024,
        }
        out = server._adapt_call_kwargs(pipe, kwargs, "stabilityai/sdxl-base")
        self.assertEqual(out, kwargs)

    def test_none_values_are_left_to_pipeline_defaults(self):
        pipe = StableDiffusionXLPipeline()
        out = server._adapt_call_kwargs(
            pipe, {"prompt": "p", "true_cfg_scale": None}, "stabilityai/sdxl-base"
        )
        self.assertEqual(out, {"prompt": "p"})

    def test_reference_edit_only_for_qwen_image21(self):
        self.assertTrue(
            server._supports_reference_edit(
                QwenImage21Pipeline(), "Qwen/Qwen-Image-2.1"
            )
        )
        self.assertFalse(
            server._supports_reference_edit(StableDiffusionXLPipeline(), "sdxl")
        )


class RequestPathTest(ServerTestCase):
    def test_generation_maps_request_guidance(self):
        pipe = self.serve(QwenImage21Pipeline(), "Qwen/Qwen-Image-2.1")
        request = server.ImageGenerationRequest(
            prompt="a red apple", size="512x512", guidance_scale=4.0, seed=7
        )
        response = self.quiet(asyncio.run, server.generate_images(request))
        self.assertEqual(len(response.data), 1)
        (call,) = pipe.calls
        self.assertNotIn("guidance_scale", call)
        self.assertEqual(call["true_cfg_scale"], 4.0)
        self.assertEqual((call["width"], call["height"]), (512, 512))
        self.assertEqual(call["num_inference_steps"], 40)

    def test_generation_sdxl_path_unchanged(self):
        pipe = self.serve(StableDiffusionXLPipeline(), "stabilityai/sdxl-base-1.0")
        request = server.ImageGenerationRequest(
            prompt="a red apple", size="512x512", negative_prompt="blurry"
        )
        self.quiet(asyncio.run, server.generate_images(request))
        (call,) = pipe.calls
        self.assertEqual(call["guidance_scale"], 7.5)
        self.assertEqual(call["negative_prompt"], "blurry")
        self.assertNotIn("true_cfg_scale", call)

    def test_warmup_passes_no_guidance_scale_to_qwen_image21(self):
        pipe = self.serve(QwenImage21Pipeline(), "Qwen/Qwen-Image-2.1")
        os.environ["WARMUP_RESOLUTIONS"] = "256x256,512x512"
        self.quiet(server.warmup_inference)
        self.assertTrue(server.warmup_complete)
        self.assertEqual(len(pipe.calls), 2)
        for call in pipe.calls:
            self.assertNotIn("guidance_scale", call)
            self.assertEqual(call["true_cfg_scale"], 1.0)

    def test_reference_edit_sends_condition_image(self):
        pipe = self.serve(QwenImage21Pipeline(), "Qwen/Qwen-Image-2.1")
        response = self.quiet(
            asyncio.run,
            edit_request(prompt="make the apple green", image=FakeUpload(png_bytes())),
        )
        self.assertEqual(len(response.data), 1)
        (call,) = pipe.calls
        self.assertEqual(len(call["image"]), 1)
        self.assertNotIn("mask_image", call)
        self.assertNotIn("strength", call)
        self.assertNotIn("width", call)

    def test_reference_edit_rejects_a_mask(self):
        self.serve(QwenImage21Pipeline(), "Qwen/Qwen-Image-2.1")
        with self.assertRaises(server.HTTPException) as caught:
            self.quiet(
                asyncio.run,
                edit_request(
                    prompt="p",
                    image=FakeUpload(png_bytes()),
                    mask=FakeUpload(png_bytes()),
                ),
            )
        self.assertEqual(caught.exception.status_code, 400)

    def test_edits_still_need_an_edit_mode_elsewhere(self):
        self.serve(StableDiffusionXLPipeline(), "stabilityai/sdxl-base-1.0")
        with self.assertRaises(server.HTTPException) as caught:
            self.quiet(asyncio.run, edit_request(prompt="p"))
        self.assertEqual(caught.exception.status_code, 400)


# --- R3: quantized component loading ----------------------------------------


class TransformerFileTest(ServerTestCase):
    def test_staged_side_file_is_found(self):
        with tempfile.TemporaryDirectory() as root:
            staged = Path(root, ".transformer", "qwen-image-2.1-UC-Q8_0.gguf")
            staged.parent.mkdir()
            staged.write_bytes(b"GGUF")
            os.environ["TRANSFORMER_FILE"] = staged.name
            os.environ["TRANSFORMER_REPO"] = "abenzerps/repo"
            self.assertEqual(
                server._resolve_transformer_file(["/nonexistent", root]), str(staged)
            )

    def test_explicit_path_wins(self):
        with tempfile.TemporaryDirectory() as root:
            explicit = Path(root, "dit.gguf")
            explicit.write_bytes(b"GGUF")
            os.environ["TRANSFORMER_PATH"] = "dit.gguf"
            self.assertEqual(server._resolve_transformer_file([root]), str(explicit))
            os.environ["TRANSFORMER_PATH"] = str(explicit)
            self.assertEqual(server._resolve_transformer_file([]), str(explicit))

    def test_unstaged_file_is_downloaded_from_its_repo(self):
        os.environ["TRANSFORMER_FILE"] = "dit.gguf"
        os.environ["TRANSFORMER_REPO"] = "abenzerps/repo"
        os.environ["TRANSFORMER_REVISION"] = "7959e658"
        hub = types.ModuleType("huggingface_hub")
        hub.hf_hub_download = mock.Mock(return_value="/cache/dit.gguf")
        with mock.patch.dict(sys.modules, {"huggingface_hub": hub}):
            path = self.quiet(server._resolve_transformer_file, ["/nonexistent"])
        self.assertEqual(path, "/cache/dit.gguf")
        hub.hf_hub_download.assert_called_once_with(
            repo_id="abenzerps/repo", filename="dit.gguf", revision="7959e658"
        )

    def test_missing_file_without_repo_raises(self):
        os.environ["TRANSFORMER_FILE"] = "dit.gguf"
        with self.assertRaisesRegex(RuntimeError, "needs a transformer file"):
            server._resolve_transformer_file(["/nonexistent"])

    def test_parent_traversal_is_rejected(self):
        os.environ["TRANSFORMER_FILE"] = "../escape.gguf"
        with self.assertRaisesRegex(RuntimeError, "inside the repo"):
            server._resolve_transformer_file(["/models"])


QWEN_MODEL_INDEX = {
    "_class_name": "QwenImage21Pipeline",
    "processor": ["transformers", "Qwen3VLProcessor"],
    "scheduler": ["diffusers", "FlowMatchEulerDiscreteScheduler"],
    "text_encoder": ["transformers", "Qwen3VLForConditionalGeneration"],
    "transformer": ["diffusers", "QwenImage21Transformer2DModel"],
    "vae": ["diffusers", "AutoencoderKLQwenImage21"],
}


class QuantizedComponentsTest(ServerTestCase):
    """Runs on stub configs in CI and on the real diffusers/transformers config
    classes where they are installed (the serving image), so a mapping value
    the real PipelineQuantizationConfig rejects fails here."""

    def setUp(self):
        super().setUp()
        self.loaded = []
        self.recorder = type(
            "QwenImage21Transformer2DModel",
            (),
            {"from_single_file": classmethod(self._from_single_file)},
        )
        self.patches = [
            mock.patch.object(
                sys.modules["diffusers"],
                "QwenImage21Transformer2DModel",
                self.recorder,
                create=True,
            ),
            mock.patch.object(
                server, "_dequantize_non_linear_gguf", return_value=["norm.weight"]
            ),
        ]
        if not REAL_DIFFUSERS:
            transformers = _stub_module("transformers")
            self.patches.append(
                mock.patch.dict(sys.modules, {"transformers": transformers})
            )
        for patch in self.patches:
            patch.start()

    def tearDown(self):
        for patch in reversed(self.patches):
            patch.stop()
        super().tearDown()

    def _from_single_file(self, cls, path, **kwargs):
        self.loaded.append((path, kwargs))
        return "gguf-transformer"

    def test_gguf_transformer_is_passed_in_as_a_component(self):
        bf16 = server.torch.bfloat16
        with tempfile.TemporaryDirectory() as root:
            staged = Path(root, ".transformer", "dit.gguf")
            staged.parent.mkdir()
            staged.write_bytes(b"GGUF")
            os.environ["TRANSFORMER_QUANTIZATION"] = "gguf"
            os.environ["TRANSFORMER_FILE"] = "dit.gguf"
            out = self.quiet(
                server._quantized_component_kwargs,
                root,
                QWEN_MODEL_INDEX,
                [root],
                bf16,
                True,
            )
        self.assertEqual(out, {"transformer": "gguf-transformer"})
        ((path, kwargs),) = self.loaded
        self.assertEqual(path, str(staged))
        self.assertEqual(kwargs["config"], root)
        self.assertEqual(kwargs["subfolder"], "transformer")
        self.assertEqual(kwargs["torch_dtype"], bf16)
        self.assertTrue(kwargs["local_files_only"])
        quant = kwargs["quantization_config"]
        self.assertIsInstance(quant, sys.modules["diffusers"].GGUFQuantizationConfig)
        self.assertEqual(quant.compute_dtype, bf16)
        server._dequantize_non_linear_gguf.assert_called_once_with(
            "gguf-transformer", bf16
        )

    def test_text_encoder_nf4_uses_the_transformers_config(self):
        os.environ["TEXT_ENCODER_QUANTIZATION"] = "nf4"
        os.environ["BNB_COMPUTE_DTYPE"] = "bfloat16"
        out = self.quiet(
            server._quantized_component_kwargs,
            "/models",
            QWEN_MODEL_INDEX,
            ["/models"],
            server.torch.bfloat16,
            True,
        )
        mapping = out["quantization_config"].quant_mapping
        self.assertEqual(sorted(mapping), ["text_encoder"])
        te_config = mapping["text_encoder"]
        self.assertIsInstance(te_config, sys.modules["transformers"].BitsAndBytesConfig)
        self.assertEqual(te_config.bnb_4bit_quant_type, "nf4")
        self.assertEqual(te_config.bnb_4bit_compute_dtype, server.torch.bfloat16)

    def test_nf4_denoiser_for_any_family_but_flux(self):
        os.environ["QUANTIZATION"] = "nf4"
        out = self.quiet(
            server._quantized_component_kwargs,
            "/models",
            QWEN_MODEL_INDEX,
            [],
            server.torch.bfloat16,
            True,
        )
        mapping = out["quantization_config"].quant_mapping
        self.assertEqual(sorted(mapping), ["transformer"])
        self.assertIsInstance(
            mapping["transformer"], sys.modules["diffusers"].BitsAndBytesConfig
        )
        flux = self.quiet(
            server._quantized_component_kwargs,
            "/models",
            {"transformer": ["diffusers", "FluxTransformer2DModel"]},
            [],
            server.torch.bfloat16,
            True,
            include_denoiser_nf4=False,
        )
        self.assertEqual(flux, {})

    def test_unknown_modes_fail_loudly(self):
        os.environ["TRANSFORMER_QUANTIZATION"] = "fp8"
        with self.assertRaisesRegex(RuntimeError, "transformerQuantization"):
            server._quantized_component_kwargs(
                "/m", {}, [], server.torch.bfloat16, True
            )
        os.environ["TRANSFORMER_QUANTIZATION"] = ""
        os.environ["TEXT_ENCODER_QUANTIZATION"] = "gguf"
        with self.assertRaisesRegex(RuntimeError, "textEncoderQuantization"):
            server._quantized_component_kwargs(
                "/m", {}, [], server.torch.bfloat16, True
            )

    def test_merge_combines_quantization_mappings(self):
        from diffusers.quantizers import PipelineQuantizationConfig

        dit = server._nf4_config("diffusers", server.torch.bfloat16)
        te = server._nf4_config("transformers", server.torch.bfloat16)
        flux_kwargs = {
            "torch_dtype": server.torch.bfloat16,
            "quantization_config": PipelineQuantizationConfig(
                quant_mapping={"transformer": dit}
            ),
        }
        merged = server._merge_component_kwargs(
            flux_kwargs,
            {
                "quantization_config": PipelineQuantizationConfig(
                    quant_mapping={"text_encoder_2": te}
                )
            },
        )
        self.assertEqual(
            merged["quantization_config"].quant_mapping,
            {"transformer": dit, "text_encoder_2": te},
        )
        self.assertEqual(
            flux_kwargs["quantization_config"].quant_mapping, {"transformer": dit}
        )


class BnbPlacementTest(ServerTestCase):
    def test_bnb_components_stay_put(self):
        module = server.torch.nn.Module

        class Quantized(module):
            is_loaded_in_4bit = True

        pipe = types.SimpleNamespace(
            components={
                "transformer": module(),
                "text_encoder": Quantized(),
                "vae": module(),
                "tokenizer": object(),
                "image_encoder": None,
            }
        )
        self.assertEqual(server._bnb_component_names(pipe), {"text_encoder"})
        os.environ["TRANSFORMER_QUANTIZATION"] = "nf4"
        self.assertEqual(
            server._bnb_component_names(pipe), {"text_encoder", "transformer"}
        )

    def test_configured_text_encoders_count_even_if_undetected(self):
        module = server.torch.nn.Module
        pipe = types.SimpleNamespace(
            components={
                "text_encoder": module(),
                "text_encoder_2": module(),
                "vae": module(),
            }
        )
        os.environ["TEXT_ENCODER_QUANTIZATION"] = "nf4"
        self.assertEqual(
            server._bnb_component_names(pipe), {"text_encoder", "text_encoder_2"}
        )


@unittest.skipUnless("torch" in STUBBED, "stubbed torch and GGUF classes only")
class GGUFShimStubTest(ServerTestCase):
    """The shim's traversal, against stand-ins for diffusers' GGUF classes."""

    def test_only_non_linear_gguf_parameters_are_dequantized(self):
        parameter = server.torch.nn.Parameter

        class GGUFParameter(parameter):
            pass

        class GGUFLinear:
            def __init__(self):
                self._parameters = {"weight": GGUFParameter("q8_0 bytes")}

            def named_parameters(self, recurse=False):
                return list(self._parameters.items())

        class Norm:
            def __init__(self, weight):
                self._parameters = {"weight": weight}

            def named_parameters(self, recurse=False):
                return list(self._parameters.items())

        class Dequantized:
            def __init__(self, source):
                self.source = source

            def to(self, dtype):
                self.dtype = dtype
                return self

        linear = GGUFLinear()
        bf16_norm = Norm(GGUFParameter("bf16 bytes"))
        f32_norm = Norm(parameter("f32"))
        model = types.SimpleNamespace(
            named_modules=lambda: [
                ("", types.SimpleNamespace(named_parameters=lambda recurse=False: [])),
                ("img_in", linear),
                ("txt_in.text_norm", bf16_norm),
                ("blocks.0.norm_q", f32_norm),
            ]
        )
        utils = types.ModuleType("diffusers.quantizers.gguf.utils")
        utils.GGUFLinear = GGUFLinear
        utils.GGUFParameter = GGUFParameter
        utils.dequantize_gguf_tensor = Dequantized
        modules = {
            "diffusers.quantizers.gguf": types.ModuleType("diffusers.quantizers.gguf"),
            "diffusers.quantizers.gguf.utils": utils,
        }
        with mock.patch.dict(sys.modules, modules):
            names = server._dequantize_non_linear_gguf(model, "bfloat16")
        self.assertEqual(names, ["txt_in.text_norm.weight"])
        new_weight = bf16_norm._parameters["weight"]
        self.assertNotIsInstance(new_weight, GGUFParameter)
        self.assertEqual(new_weight.data.source.data, "bf16 bytes")
        self.assertEqual(new_weight.data.dtype, "bfloat16")
        self.assertFalse(new_weight.requires_grad)
        self.assertIsInstance(linear._parameters["weight"], GGUFParameter)
        self.assertEqual(f32_norm._parameters["weight"].data, "f32")


# --- real libraries ---------------------------------------------------------


@unittest.skipUnless(
    REAL_DIFFUSERS and _real_qwen_image21() is not None,
    "needs torch and diffusers >= 0.41",
)
class RealLibrariesTest(ServerTestCase):
    def test_qwen_image21_signature_matches_the_contract(self):
        pipeline_cls = _real_qwen_image21()
        params = inspect.signature(pipeline_cls.__call__).parameters
        self.assertNotIn("guidance_scale", params)
        for name in ("true_cfg_scale", "negative_prompt", "image", "output_resolution"):
            self.assertIn(name, params)
        pipe = object.__new__(pipeline_cls)
        out = self.quiet(
            server._adapt_call_kwargs,
            pipe,
            {"prompt": "p", "guidance_scale": 4.0, "num_inference_steps": 4},
            "Qwen/Qwen-Image-2.1",
        )
        self.assertEqual(
            out,
            {
                "prompt": "p",
                "num_inference_steps": 4,
                "true_cfg_scale": 4.0,
                "negative_prompt": " ",
            },
        )

    def test_gguf_shim_dequantizes_a_bf16_norm(self):
        import gguf
        import torch
        from diffusers.quantizers.gguf.utils import GGUFLinear, GGUFParameter

        class Toy(torch.nn.Module):
            def __init__(self):
                super().__init__()
                self.norm = torch.nn.RMSNorm(8)
                self.proj = GGUFLinear(8, 8, bias=False, compute_dtype=torch.bfloat16)

        model = Toy()
        expected = torch.linspace(-1, 1, 8, dtype=torch.bfloat16)
        raw = expected.view(
            torch.uint8
        )  # [8] bf16 -> uint8 [16], as diffusers stores it
        model.norm.weight = GGUFParameter(
            raw, quant_type=gguf.GGMLQuantizationType.BF16
        )
        linear_weight = model.proj.weight
        names = server._dequantize_non_linear_gguf(model, torch.bfloat16)
        self.assertEqual(names, ["norm.weight"])
        self.assertNotIsInstance(model.norm.weight, GGUFParameter)
        self.assertEqual(tuple(model.norm.weight.shape), (8,))
        self.assertTrue(torch.equal(model.norm.weight.data, expected))
        self.assertIs(model.proj.weight, linear_weight)
        model.norm(torch.ones(1, 8, dtype=torch.bfloat16))  # raised before the shim


if __name__ == "__main__":
    print(
        f"stubbed: {sorted(STUBBED) or 'none'}; real diffusers: {REAL_DIFFUSERS}",
        flush=True,
    )
    unittest.main(verbosity=2)
