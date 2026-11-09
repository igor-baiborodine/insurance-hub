"""Independent state-transition tests for exclusive Dashboard ownership."""

import ownership_transition as transition
import pytest

SAFE_PROGRESS = transition.ProgressState(stable=True, complete=True, in_range=True)


def state(**changes: int) -> transition.OwnershipState:
    values = {
        "java_running": 0,
        "java_terminating": 0,
        "go_running": 0,
        "go_terminating": 0,
        "canonical_members": 0,
    }
    values.update(changes)
    return transition.OwnershipState(**values)


def test_successor_start_requires_complete_quiescence_and_progress() -> None:
    transition.require_successor_start(state(), SAFE_PROGRESS)
    for observed in (
        state(java_running=1, canonical_members=1),
        state(java_terminating=1),
        state(go_running=1, canonical_members=1),
        state(canonical_members=1),
    ):
        with pytest.raises(transition.TransitionError):
            transition.require_successor_start(observed, SAFE_PROGRESS)
    for progress in (
        transition.ProgressState(False, True, True),
        transition.ProgressState(True, False, True),
        transition.ProgressState(True, True, False),
    ):
        with pytest.raises(transition.TransitionError):
            transition.require_successor_start(state(), progress)


def test_interrupted_transition_is_classified_before_retry() -> None:
    assert transition.phase(state(java_terminating=1)) == "terminating"
    assert transition.phase(state()) == "quiescent"
    assert transition.phase(state(go_running=1, canonical_members=1)) == "go-owned"
    with pytest.raises(transition.TransitionError, match="overlap"):
        transition.phase(state(java_running=1, go_running=1, canonical_members=2))


def test_return_uses_the_same_quiescence_boundary_and_finite_limits() -> None:
    transition.require_predecessor_return(state(), SAFE_PROGRESS)
    with pytest.raises(transition.TransitionError):
        transition.require_predecessor_return(state(go_terminating=1), SAFE_PROGRESS)
    assert transition.LIMITS == {
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
