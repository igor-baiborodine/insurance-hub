#!/usr/bin/env python3
"""Test generic Go release selection and QA semantic-tag updates."""

from __future__ import annotations

from pathlib import Path
import sys
import tempfile
import unittest


sys.path.insert(0, str(Path(__file__).parent))
import release  # noqa: E402

REPOSITORY = "igor-baiborodine/insurance-hub"


class ReleaseSelectionTests(unittest.TestCase):
    def fixture(self, root: Path, service: str, *, qa_entry: bool = True) -> None:
        module = root / "services" / f"{service}-service"
        module.mkdir(parents=True)
        (module / "Dockerfile").write_text("FROM scratch\n", encoding="utf-8")
        overlay = root / "k8s" / "overlays" / "qa" / "svc" / service
        overlay.mkdir(parents=True)
        image = service if qa_entry else "other"
        (overlay / "kustomization.yaml").write_text(
            "apiVersion: kustomize.config.k8s.io/v1beta1\n"
            "kind: Kustomization\n"
            "images:\n"
            f"  - name: insurance-hub-{image}-api\n"
            f"    newName: ghcr.io/{REPOSITORY}/{image}-api\n"
            "    newTag: sha-unpublished\n",
            encoding="utf-8",
        )

    def test_product_and_future_service_use_the_same_convention(self) -> None:
        with tempfile.TemporaryDirectory(prefix="go-service-release-test.") as directory:
            root = Path(directory)
            for service in ("product", "claims"):
                self.fixture(root, service)
                selected = release.select_service(
                    [f"services/{service}-service/internal/app.go"], root, REPOSITORY,
                )
                self.assertEqual(selected.service, service)
                self.assertEqual(selected.module_path, f"services/{service}-service")
                self.assertEqual(selected.qa_kustomization,
                                 f"k8s/overlays/qa/svc/{service}/kustomization.yaml")

    def test_zero_and_multiple_service_selections_fail(self) -> None:
        with tempfile.TemporaryDirectory(prefix="go-service-release-test.") as directory:
            root = Path(directory)
            self.fixture(root, "product")
            self.fixture(root, "claims")
            for paths in ([], ["k8s/overlays/qa/svc/product/kustomization.yaml"], [
                "services/product-service/main.go", "services/claims-service/main.go",
            ]):
                with self.subTest(paths=paths), self.assertRaises(release.ReleaseSelectionError):
                    release.select_service(paths, root, REPOSITORY)

    def test_missing_standard_qa_entry_fails(self) -> None:
        with tempfile.TemporaryDirectory(prefix="go-service-release-test.") as directory:
            root = Path(directory)
            self.fixture(root, "product", qa_entry=False)
            with self.assertRaisesRegex(release.ReleaseSelectionError, "exactly one"):
                release.select_service(["services/product-service/main.go"], root, REPOSITORY)

    def test_update_changes_only_the_selected_semantic_tag(self) -> None:
        with tempfile.TemporaryDirectory(prefix="go-service-release-test.") as directory:
            root = Path(directory)
            self.fixture(root, "product")
            path = release.update_qa_tag(root, "product", REPOSITORY, "1.2.3")
            text = path.read_text(encoding="utf-8")
            self.assertIn("newTag: 1.2.3", text)
            self.assertNotIn("sha-unpublished", text)
            with self.assertRaises(release.ReleaseSelectionError):
                release.update_qa_tag(root, "product", REPOSITORY, "2.0.0")


if __name__ == "__main__":
    unittest.main()
