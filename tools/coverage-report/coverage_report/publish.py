"""Atomic output publication.

A report is written into a temporary sibling directory and only then swapped
into place. If anything fails, the previously published report must remain
readable and complete: a half-written report is worse than a stale one.

Swap sequence:

1. Write every artifact into ``<output_dir>/.<name>.tmp-*``.
2. If the target exists, rename it to ``<output_dir>/.<name>.bak-*``.
3. Rename the temporary directory to the target.
4. Delete the backup.

A failure at step 1 leaves the target untouched. A failure at step 3 renames the
backup back. The ``fault_hook`` parameter exists so tests can inject failures at
each named stage without monkeypatching private functions.
"""

import os
import shutil
import tempfile
from typing import Callable, Dict, Optional

from .errors import PublishError
from .serialize import write_json, write_text_atomic

FAULT_STAGES = ("after_write", "after_backup", "after_swap")

FaultHook = Callable[[str], None]


def _check_fault_hook(hook: Optional[FaultHook], stage: str) -> None:
    if hook is not None:
        hook(stage)


def _write_artifacts(
    directory: str,
    *,
    json_artifacts: Dict[str, object],
    text_artifacts: Dict[str, str],
    input_copies: Dict[str, str],
) -> None:
    for relative_path in sorted(json_artifacts):
        write_json_relative(directory, relative_path, json_artifacts[relative_path])
    for relative_path in sorted(text_artifacts):
        write_text_relative(directory, relative_path, text_artifacts[relative_path])
    for relative_path in sorted(input_copies):
        _copy_file(directory, relative_path, input_copies[relative_path])


def _ensure_parent(directory: str, relative_path: str) -> str:
    if os.path.isabs(relative_path) or ".." in relative_path.split("/"):
        raise PublishError(
            "refusing to write artifact outside the output directory: {0}".format(
                relative_path
            ),
            recovery="Use a relative artifact path without '..'.",
        )
    destination = os.path.join(directory, relative_path)
    parent = os.path.dirname(destination)
    if parent:
        os.makedirs(parent, exist_ok=True)
    return destination


def write_json_relative(directory: str, relative_path: str, value: object) -> None:
    """Write canonical JSON at ``directory/relative_path``."""
    write_json(_ensure_parent(directory, relative_path), value)


def write_text_relative(directory: str, relative_path: str, text: str) -> None:
    """Write text at ``directory/relative_path``."""
    write_text_atomic(_ensure_parent(directory, relative_path), text)


def _copy_file(directory: str, relative_path: str, source: str) -> None:
    destination = _ensure_parent(directory, relative_path)
    try:
        shutil.copyfile(source, destination)
    except OSError as error:
        raise PublishError(
            "failed to copy input {0}: {1}".format(source, error),
            code="INPUT_COPY_FAILED",
            recovery="Check that the input file still exists and is readable.",
        )


def _remove_path(path: str) -> None:
    """Remove a file or directory tree, ignoring absence."""
    if os.path.isdir(path) and not os.path.islink(path):
        shutil.rmtree(path, ignore_errors=True)
        return
    try:
        os.unlink(path)
    except FileNotFoundError:
        pass


def publish(
    output_dir: str,
    name: str,
    *,
    json_artifacts: Dict[str, object],
    text_artifacts: Dict[str, str],
    input_copies: Optional[Dict[str, str]] = None,
    fault_hook: Optional[FaultHook] = None,
) -> str:
    """Atomically publish one report directory.

    Args:
        output_dir: Parent directory that holds one subdirectory per report.
        name: Report directory name (a feature key or ``ALL``).
        json_artifacts: Relative path -> JSON-serializable value.
        text_artifacts: Relative path -> text content.
        input_copies: Relative path -> source file to copy.
        fault_hook: Test hook called with a stage name from ``FAULT_STAGES``.

    Returns:
        Absolute path to the published directory.

    Raises:
        PublishError: When the output cannot be written. The previous report is
            restored before the error is raised.
    """
    output_dir = os.path.abspath(output_dir)
    target = os.path.join(output_dir, name)
    try:
        os.makedirs(output_dir, exist_ok=True)
        working = tempfile.mkdtemp(prefix=".{0}.tmp-".format(name), dir=output_dir)
    except OSError as error:
        raise PublishError(
            "could not prepare output directory {0}: {1}".format(output_dir, error),
            recovery="Check write permissions on {0}.".format(output_dir),
        )

    backup: Optional[str] = None
    moved_backup = False
    swapped = False
    succeeded = False
    try:
        _write_artifacts(
            working,
            json_artifacts=json_artifacts,
            text_artifacts=text_artifacts,
            input_copies=input_copies or {},
        )
        _check_fault_hook(fault_hook, "after_write")

        if os.path.exists(target):
            backup = tempfile.mkdtemp(
                prefix=".{0}.bak-".format(name), dir=output_dir
            )
            os.rmdir(backup)
            os.rename(target, backup)
            moved_backup = True
        _check_fault_hook(fault_hook, "after_backup")

        os.rename(working, target)
        working = ""
        swapped = True
        _check_fault_hook(fault_hook, "after_swap")
        succeeded = True
    except BaseException as error:
        # Restore the previously published report, if there was one, so a
        # failed publish never leaves a partial or missing output behind.
        if swapped:
            _remove_path(target)
            swapped = False
        restore_failure: Optional[OSError] = None
        if moved_backup and backup is not None and not os.path.exists(target):
            try:
                os.rename(backup, target)
                moved_backup = False
            except OSError as restore_error:
                restore_failure = restore_error
        if restore_failure is not None:
            raise PublishError(
                "failed to publish report to {0}: {1}; restoring the previous "
                "report also failed: {2}".format(target, error, restore_failure),
                code="PUBLISH_RESTORE_FAILED",
                recovery="The previous report is preserved at {0}; move it back "
                "to {1} manually and re-run.".format(backup, target),
            ) from error
        if isinstance(error, PublishError):
            raise
        raise PublishError(
            "failed to publish report to {0}: {1}".format(target, error),
            code="PUBLISH_FAILED",
            recovery="The previous report, if any, was left in place. "
            "Fix the underlying error and re-run.",
        ) from error
    finally:
        if working:
            shutil.rmtree(working, ignore_errors=True)
        # Keep the backup when the swap did not complete and a restore could
        # not put it back; deleting it would lose the previous report.
        if succeeded and moved_backup and backup is not None:
            _remove_path(backup)

    return target
