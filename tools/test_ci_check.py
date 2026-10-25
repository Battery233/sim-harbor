import unittest
from pathlib import Path
from tempfile import TemporaryDirectory

from ci_check import anchors, changed_scopes, check_links, versions


class RepositoryChecks(unittest.TestCase):
    def test_changed_scopes_do_not_skip_shared_changes(self):
        self.assertEqual(changed_scopes(["docs/voicemail.md"]), (False, False))
        self.assertEqual(changed_scopes(["internal/store/calls.go"]), (True, False))
        self.assertEqual(changed_scopes(["web/src/App.vue"]), (False, True))
        self.assertEqual(changed_scopes(["Dockerfile", "tools/ci_check.py"]), (True, True))

    def test_links_support_chinese_duplicates_and_ignore_remote_links(self):
        self.assertEqual(anchors("# 中文、标题\n# 中文、标题"), {"中文标题", "中文标题-1"})
        with TemporaryDirectory() as directory:
            root = Path(directory)
            (root / "a.md").write_text("# 中文、标题\n[链接](#中文标题) [远程](https://example.invalid/)")
            self.assertEqual(check_links(root, ["a.md"]), 1)
            (root / "a.md").write_text("[错误](missing.md)")
            with self.assertRaises(ValueError):
                check_links(root, ["a.md"])

    def test_existing_version_agrees_and_mismatches_fail(self):
        source = Path(__file__).resolve().parent.parent
        with TemporaryDirectory() as directory:
            root = Path(directory)
            for name in ("Dockerfile", "compose.yaml", "go.mod", "internal/version/version.go", "web/package.json", "web/package-lock.json"):
                path = root / name
                path.parent.mkdir(parents=True, exist_ok=True)
                path.write_bytes((source / name).read_bytes())
            node = versions(root)
            self.assertRegex(node, r"^\d+\.\d+\.\d+$")
            path = root / "Dockerfile"
            original = path.read_text()
            legacy = original.replace("FROM --platform=$BUILDPLATFORM node:", "FROM node:", 1)
            for docker in (original, legacy):
                with self.subTest(native_builder="--platform=$BUILDPLATFORM" in docker):
                    path.write_text(docker)
                    self.assertEqual(versions(root), node)
                    path.write_text(docker.replace(f"node:{node}-", "node:alpine-", 1))
                    with self.assertRaisesRegex(ValueError, "pinned Node version"):
                        versions(root)
            path.write_text(original)
            path = root / "compose.yaml"
            original = path.read_text()
            for invalid in ("image: ${SIM_IMAGE:-sim-harbor:0.0.0}", "image: sim-harbor:0.0.0"):
                with self.subTest(compose=invalid):
                    path.write_text("services:\n  sim:\n    " + invalid + "\n")
                    with self.assertRaisesRegex(ValueError, "Compose default image"):
                        versions(root)
            path.write_text(original)
            path = root / "internal/version/version.go"
            path.write_text('package version\nconst Version = "0.0.0"\n')
            with self.assertRaises(ValueError):
                versions(root)


if __name__ == "__main__":
    unittest.main()
