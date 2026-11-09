"""Pure ownership-transfer guards for the Dashboard Java and Go workloads."""

from __future__ import annotations

from dataclasses import dataclass


class TransitionError(RuntimeError):
    """Observed ownership state cannot safely advance."""


@dataclass(frozen=True)
class OwnershipState:
    java_running: int
    java_terminating: int
    go_running: int
    go_terminating: int
    canonical_members: int

    def __post_init__(self) -> None:
        if any(value < 0 for value in self.__dict__.values()):
            raise TransitionError("ownership counts must be non-negative")


@dataclass(frozen=True)
class ProgressState:
    stable: bool
    complete: bool
    in_range: bool


LIMITS = {
    "local-dev": {
        "action_seconds": 180,
        "return_seconds": 600,
        "abort_classify_seconds": 60,
        "abort_initiate_seconds": 120,
    },
    "qa": {
        "action_seconds": 600,
        "return_seconds": 1800,
        "abort_classify_seconds": 60,
        "abort_initiate_seconds": 120,
    },
}


def phase(state: OwnershipState) -> str:
    """Classify observed state so an interrupted operation can be resumed safely."""
    if state.java_running and state.go_running:
        raise TransitionError("Java and Go ownership overlap")
    if state.java_terminating or state.go_terminating:
        return "terminating"
    if state.java_running:
        if state.canonical_members:
            return "java-owned"
        raise TransitionError("Java is running without its canonical group member")
    if state.go_running:
        if state.go_running == 1 and state.canonical_members == 1:
            return "go-owned"
        raise TransitionError("Go canonical membership differs")
    if state.canonical_members:
        raise TransitionError("canonical group has an unexpected member")
    return "quiescent"


def require_successor_start(state: OwnershipState, progress: ProgressState) -> None:
    """Allow Go canonical startup only at the fully observed quiescent boundary."""
    if phase(state) != "quiescent":
        raise TransitionError("successor start requires quiescence")
    if not (progress.stable and progress.complete and progress.in_range):
        raise TransitionError("canonical committed progress is not safe")


def require_predecessor_return(state: OwnershipState, progress: ProgressState) -> None:
    """Allow Java return only after Go and its group membership are gone."""
    if phase(state) != "quiescent":
        raise TransitionError("predecessor return requires quiescence")
    if not (progress.stable and progress.complete and progress.in_range):
        raise TransitionError("canonical committed progress is not safe")
