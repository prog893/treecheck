# NVMe Integrity Checker

A bash script that creates and verifies SHA-256 sidecar files for data integrity checking. Perfect for protecting important files on NVMe drives, external storage, or any filesystem.

## Features

- **Safe operation** - Never deletes or modifies original files
- **Multiple modes** - Verify-only, create+verify, create-only
- **Flexible exclusions** - Skip system directories or unwanted paths
- **Recursive processing** - Handle entire directory trees
- **Clear reporting** - Shows hash mismatches, missing files, and summary stats

## Usage

```bash
./nvme-integrity-checker.sh [OPTIONS] <directory>
```

### Options

- `-c` Create hashes + verify existing (default: verify only)
- `-f` Force overwrite existing hashes
- `-r` Recursive
- `-e` Exclude dirs (comma-separated)
- `-n` Skip verify (requires `-c`, creates sidecars without verification)
- `-v` Verbose
- `-h` Help

### Mode Combinations

- **No flags** → Verify only
- **`-c`** → Create and verify
- **`-c -n`** → Create only, skip verify
- **`-n` without `-c`** → Does nothing (exits with "Nothing to do" message)

## Examples

### Verify-Only Mode (Default)
Check existing sidecar files for integrity violations:
```bash
# Verify files in current directory
./nvme-integrity-checker.sh .

# Verify recursively with verbose output
./nvme-integrity-checker.sh -rv /path/to/data

# Verify excluding specific directories
./nvme-integrity-checker.sh -rv -e "temp,cache" /Volumes/MyDrive
```

### Create + Verify Mode
Create new sidecars and verify existing ones:
```bash
# Create sidecars for new files, verify existing ones
./nvme-integrity-checker.sh -cr /path/to/data

# With exclusions and verbose output
./nvme-integrity-checker.sh -crv -e "temp,logs,cache" /path/to/data
```

### Create-Only Mode
Create sidecars without verification (requires both `-c` and `-n`):
```bash
# Fast mode - only create missing sidecars, no verification
./nvme-integrity-checker.sh -cnr /path/to/data

# Useful for initial setup or adding sidecars to new files quickly
./nvme-integrity-checker.sh -cnrv -e "temp,logs" /Volumes/ExternalDrive

# Note: Using -n without -c will exit with "Nothing to do"
```

## Output Examples

### Successful Verification
```
Mode: Verify only | Dir: /data | Recursive: Yes

✓ Verified: /data/video.mp4
✓ Verified: /data/audio.wav

Summary: 2 files, 2 verified, 0 failed
✓ Completed successfully
```

### Hash Mismatch Detection
```
Mode: Verify only | Dir: /data | Recursive: Yes

✓ Verified: /data/good_file.txt
HASH MISMATCH: corrupted_file.txt

Summary: 2 files, 1 verified, 1 failed
ERROR: Completed with errors
```

### Create + Verify Mode
```
Mode: Create + Verify | Dir: /data | Recursive: Yes

Hash exists for: /data/old_file.txt
Verifying: /data/old_file.txt
✓ Verified: /data/old_file.txt
Hashing: /data/new_file.txt
Created: /data/new_file.txt.sha256
Verifying: /data/new_file.txt
✓ Verified: /data/new_file.txt

Summary: 2 files, 2 created, 0 failed
✓ Completed successfully
```

### Create-Only Mode (Skip Verify)
```
Mode: Create only (skip verify) | Dir: /data | Recursive: Yes

Hashing: /data/new_file1.txt
Created: /data/new_file1.txt.sha256
Hashing: /data/new_file2.txt
Created: /data/new_file2.txt.sha256

Summary: 2 files, 2 created, 0 failed
✓ Completed successfully
```

## How It Works

1. **Sidecar files**: Creates `.sha256` files alongside your data files
2. **SHA-256 hashing**: Uses cryptographically secure hashing
3. **Non-destructive**: Only creates new files, never modifies originals
4. **Smart skipping**: Automatically skips `.sha256` files and dot files/directories

## Use Cases

- **NVMe drive integrity monitoring** - Detect silent data corruption
- **Backup verification** - Ensure backups haven't been corrupted
- **Archive integrity** - Long-term storage verification
- **Transfer verification** - Confirm files copied correctly
- **Media protection** - Protect video/audio files from corruption

## Requirements

- Bash shell
- `shasum` command (standard on macOS/Linux)
- `find` command with `-print0` support
