#if !defined(__HIP_PLATFORM_AMD__) && !defined(__HIP_PLATFORM_NVIDIA__)
#define __HIP_PLATFORM_AMD__
#endif

#include <hip/hip_runtime_api.h>

#include <dirent.h>
#include <dlfcn.h>
#include <stdint.h>

#include <cstdio>
#include <cstdlib>
#include <cstring>
#include <string>

namespace {

using HipMemGetInfoFn = hipError_t (*)(size_t *, size_t *);

bool read_u64_file(const std::string &path, uint64_t *value) {
    FILE *file = std::fopen(path.c_str(), "r");
    if (file == nullptr) {
        return false;
    }

    unsigned long long parsed = 0;
    const int matched = std::fscanf(file, "%llu", &parsed);
    std::fclose(file);
    if (matched != 1) {
        return false;
    }

    *value = static_cast<uint64_t>(parsed);
    return true;
}

bool read_sysfs_vram(size_t *free_bytes, size_t *total_bytes) {
    DIR *dir = opendir("/sys/class/drm");
    if (dir == nullptr) {
        return false;
    }

    uint64_t best_total = 0;
    uint64_t best_used = 0;

    for (dirent *entry = readdir(dir); entry != nullptr; entry = readdir(dir)) {
        const char *name = entry->d_name;
        if (std::strncmp(name, "card", 4) != 0 || std::strchr(name, '-') != nullptr) {
            continue;
        }

        const std::string base = std::string("/sys/class/drm/") + name + "/device/";
        uint64_t total = 0;
        if (!read_u64_file(base + "mem_info_vram_total", &total) || total == 0) {
            continue;
        }

        uint64_t used = 0;
        (void)read_u64_file(base + "mem_info_vram_used", &used);

        if (total > best_total) {
            best_total = total;
            best_used = used;
        }
    }

    closedir(dir);
    if (best_total == 0) {
        return false;
    }

    *total_bytes = static_cast<size_t>(best_total);
    *free_bytes = static_cast<size_t>(best_used < best_total ? best_total - best_used : 0);
    return true;
}

size_t fallback_total_bytes() {
    const char *override_mb = std::getenv("FLEXINFER_HIPMEMINFO_FALLBACK_TOTAL_MB");
    char *end = nullptr;
    const unsigned long long parsed = override_mb == nullptr ? 0 : std::strtoull(override_mb, &end, 10);
    if (parsed > 0 && end != override_mb) {
        return static_cast<size_t>(parsed) * 1024ULL * 1024ULL;
    }
    return 16ULL * 1024ULL * 1024ULL * 1024ULL;
}

void maybe_log_fallback(hipError_t err, size_t free_bytes, size_t total_bytes) {
    const char *log = std::getenv("FLEXINFER_HIPMEMINFO_SHIM_LOG");
    if (log != nullptr && std::strcmp(log, "0") == 0) {
        return;
    }

    std::fprintf(
        stderr,
        "[flexinfer-hipmeminfo-shim] hipMemGetInfo failed err=%d; returning free=%zu total=%zu\n",
        static_cast<int>(err),
        free_bytes,
        total_bytes);
}

HipMemGetInfoFn real_hip_mem_get_info() {
    static HipMemGetInfoFn fn = reinterpret_cast<HipMemGetInfoFn>(dlsym(RTLD_NEXT, "hipMemGetInfo"));
    return fn;
}

} // namespace

extern "C" hipError_t hipMemGetInfo(size_t *free_bytes, size_t *total_bytes) {
    if (free_bytes == nullptr || total_bytes == nullptr) {
        return hipErrorInvalidValue;
    }

    HipMemGetInfoFn real_fn = real_hip_mem_get_info();
    if (real_fn != nullptr) {
        const hipError_t err = real_fn(free_bytes, total_bytes);
        if (err == hipSuccess && *total_bytes > 0) {
            return hipSuccess;
        }
        if (read_sysfs_vram(free_bytes, total_bytes)) {
            maybe_log_fallback(err, *free_bytes, *total_bytes);
            return hipSuccess;
        }

        *total_bytes = fallback_total_bytes();
        *free_bytes = *total_bytes;
        maybe_log_fallback(err, *free_bytes, *total_bytes);
        return hipSuccess;
    }

    if (read_sysfs_vram(free_bytes, total_bytes)) {
        maybe_log_fallback(hipErrorInvalidValue, *free_bytes, *total_bytes);
        return hipSuccess;
    }

    *total_bytes = fallback_total_bytes();
    *free_bytes = *total_bytes;
    maybe_log_fallback(hipErrorInvalidValue, *free_bytes, *total_bytes);
    return hipSuccess;
}
