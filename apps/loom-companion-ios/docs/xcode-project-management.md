# Xcode Project File Management (pbxproj)

## Overview

The Loom Companion iOS app uses a `.xcodeproj` managed by **xcodegen** from `project.yml`. When files are created outside of Xcode, the project must be regenerated to pick them up.

**If you create a new `.swift` file on disk but don't regenerate the project, Xcode will not compile it, and you'll get "Cannot find type in scope" errors.**

## Primary Method: xcodegen (Recommended)

The project uses `project.yml` with `createIntermediateGroups: true`, which means xcodegen **auto-discovers all `.swift` files** in the `Sources/` directory tree. Simply run:

```bash
make mobile-ios-project-sync
```

This regenerates `project.pbxproj` from `project.yml`. No manual ID management needed.

### When to use xcodegen
- After creating any new `.swift` file
- After moving/renaming files
- After modifying `project.yml` (e.g., adding a new target, changing settings)

### When xcodegen is NOT enough
- If you need custom build phase scripts or per-file compiler flags
- In those rare cases, use the manual approach or `scripts/add_to_xcodeproj.py`

## Fallback: pbxproj Anatomy

The `project.pbxproj` file is a NeXT-style property list with these key sections:

### 1. PBXBuildFile (line ~9)

Maps a build file reference to a file reference. One entry per file per target.

```
BUILDFILE_ID /* Filename.swift in Sources */ = {
    isa = PBXBuildFile;
    fileRef = FILEREF_ID /* Filename.swift */;
};
```

### 2. PBXFileReference (line ~162)

Declares the file's existence, type, and path. One entry per file (shared across targets).

```
FILEREF_ID /* Filename.swift */ = {
    isa = PBXFileReference;
    lastKnownFileType = sourcecode.swift;
    path = Filename.swift;
    sourceTree = "<group>";
};
```

### 3. PBXGroup (line ~289)

Organizes files into the Xcode project navigator tree. Files must be added to the correct group's `children` array to appear in the right folder.

```
GROUP_ID /* GroupName */ = {
    isa = PBXGroup;
    children = (
        FILEREF_ID /* Filename.swift */,
    );
    path = GroupName;
    sourceTree = "<group>";
};
```

### 4. PBXSourcesBuildPhase (line ~708)

Lists which build files are compiled for each target. Each target has its own Sources build phase.

```
SOURCES_PHASE_ID /* Sources */ = {
    isa = PBXSourcesBuildPhase;
    files = (
        BUILDFILE_ID /* Filename.swift in Sources */,
    );
};
```

## Target Map

| Target | Native Target ID | Sources Phase ID | Purpose |
|--------|-----------------|-----------------|---------|
| **LoomCompanion** (app) | `206507807B35EBD83FDA7303` | `03D7A70606FB4EFE9B648B98` | Main iOS app |
| **LoomCompanionKit** (framework) | `A1E3786F0E278DF274CEE75D` | `F26E08CF7C6C423A2D91DA7D` | Shared models, networking, view models |
| **LoomCompanionWidget** (extension) | `6750481BF789286A3C05F0E6` | `0810009D742E099841700DCC` | Widget extension + Live Activities |

## Group Map (commonly used)

| Group | Group ID | Target | Path |
|-------|----------|--------|------|
| Models | `A844B9E2031F24E4BAE6BAC3` | Kit | `Sources/LoomCompanionKit/Models/` |
| Networking | `DE723BFA102C44EB318ACE38` | Kit | `Sources/LoomCompanionKit/Networking/` |
| Services | `2C5B5E8E4ED8B1A15914E3D1` | Kit | `Sources/LoomCompanionKit/Services/` |
| ViewModels | `B39CFD1AD6E2DA65E866D2C5` | Kit | `Sources/LoomCompanionKit/ViewModels/` |
| Components | `FF004545B6ED4262B66A78D8` | App | `Sources/LoomCompanion/Components/` |
| Charts | `C1409E72DAFBDC07CFAADC43` | App | `Sources/LoomCompanion/Components/Charts/` |
| DesignSystem | `7C0185E86B5B88DAC95F5BD1` | App | `Sources/LoomCompanion/DesignSystem/` |
| Navigation | `8E3500C5EC42603FF8B38E9C` | App | `Sources/LoomCompanion/Navigation/` |
| Views | `8717CC413A4AEEB0AE0B2CB4` | App | `Sources/LoomCompanion/Views/` |
| Agents | `3C10E1824C1DB7C7D463EB2F` | App | `Sources/LoomCompanion/Views/Agents/` |
| Alerts | `F266D3641533B7B6917C6293` | App | `Sources/LoomCompanion/Views/Alerts/` |
| Connection | `2BE3A5662F9DA71DE25EC26C` | App | `Sources/LoomCompanion/Views/Connection/` |
| Dashboard | `E799C002B5D57AB6690F2D63` | App | `Sources/LoomCompanion/Views/Dashboard/` |
| Ops | `94E138E964ADC74E7EBF3410` | App | `Sources/LoomCompanion/Views/Ops/` |
| SessionDetail | `A4929F479D971135B1566B70` | App | `Sources/LoomCompanion/Views/SessionDetail/` |
| Sessions | `9D4BCF9F36EB467A7AC99AF4` | App | `Sources/LoomCompanion/Views/Sessions/` |
| Shared | `9E3AB81F7F45D768CE409B3E` | App | `Sources/LoomCompanion/Views/Shared/` |
| Spawn | `5B5C969B517E14D8E56AA2A2` | App | `Sources/LoomCompanion/Views/Spawn/` |
| Intents | `930A6B883E50FDE5E0D27427` | App | `Sources/LoomCompanion/Intents/` |
| Utilities | `093267C2E51E16FB5623E69D` | App | `Sources/LoomCompanion/Utilities/` |
| LoomCompanionWidget | `1DDDBF31AFD42FED125F68FB` | Widget | `Sources/LoomCompanionWidget/` |

## Adding a New File

### Step-by-step (4 insertions needed)

For each new `.swift` file, you need to add entries in 4 places:

1. **Generate two unique 24-char hex IDs** — one for PBXFileReference, one for PBXBuildFile
2. **PBXBuildFile section** — add before `/* End PBXBuildFile section */`
3. **PBXFileReference section** — add before `/* End PBXFileReference section */`
4. **PBXGroup children** — add the fileRef ID to the correct group's `children = (...)` array
5. **PBXSourcesBuildPhase files** — add the buildFile ID to the correct target's `files = (...)` array

### Automated Script

Use the Python script at `scripts/add_to_xcodeproj.py`:

```bash
python3 scripts/add_to_xcodeproj.py \
    --file MyNewView.swift \
    --group-id FF004545B6ED4262B66A78D8 \
    --sources-id 03D7A70606FB4EFE9B648B98
```

Or for multiple files:

```bash
python3 scripts/add_to_xcodeproj.py \
    --file SessionActivity.swift --group-id A844B9E2031F24E4BAE6BAC3 --sources-id F26E08CF7C6C423A2D91DA7D \
    --file PipelineActivity.swift --group-id A844B9E2031F24E4BAE6BAC3 --sources-id F26E08CF7C6C423A2D91DA7D
```

### ID Generation

IDs must be:
- Exactly 24 hexadecimal characters (uppercase)
- Unique within the entire project file
- Deterministic generation recommended (e.g., `md5(seed)[:24]`) to avoid duplicates across sessions

### Common Pitfalls

1. **Regex matching wrong groups**: Group IDs appear as both references (in parent group's `children`) and definitions (in their own `= {` block). Always match on `ID /* Name */ = {` (the definition form), not just the ID.

2. **File in wrong group**: Xcode resolves file paths relative to the group hierarchy. A file referenced in the wrong group will produce "Build input files cannot be found" errors.

3. **Missing from Sources build phase**: File exists in project navigator but isn't compiled. You'll get "Cannot find type in scope" errors.

4. **Wrong target**: Models/networking go in LoomCompanionKit, views go in LoomCompanion, widget views go in LoomCompanionWidget.

## Verification

After modifying the pbxproj, always verify with a build:

```bash
xcodebuild -project LoomCompanion.xcodeproj \
    -scheme LoomCompanion \
    -sdk iphonesimulator \
    -destination 'platform=iOS Simulator,name=iPhone 17 Pro,OS=26.2' \
    build 2>&1 | grep -E "error:|BUILD"
```

## Alternative: Swift Package Manager Migration

To avoid pbxproj management entirely, the project could be migrated to use a `Package.swift` with SPM targets. SPM auto-discovers source files by directory convention — no manual registration needed. This is a significant migration but eliminates the class of bugs described above.
