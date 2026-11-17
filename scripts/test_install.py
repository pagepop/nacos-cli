import hashlib
import io
import os
from pathlib import Path
import platform
import re
import shutil
import subprocess
import sys
import tarfile
import tempfile
import unittest


INSTALLER = Path(__file__).with_name("install.sh")


class InstallerCapabilities(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory(prefix="nacos-installer-test-")
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name)
        self.project = self.root / "separate project"
        self.project.mkdir()
        self.target = self.project / "bin/nacos-cli"
        self.target.parent.mkdir()
        self.old = b"#!/bin/sh\necho controlled-old\n"
        self.target.write_bytes(self.old)
        self.target.chmod(0o755)
        self.version = "1.1.4-pagepop.2"
        self.binary_version = self.version
        self.help_text = "Usage:\n  nacos-cli role-list [flags]\n  --all\n  --output string\n"
        self.harness = self.root / "harness"
        self.harness.mkdir()
        curl = self.harness / "curl"
        curl.write_text(f"#!{sys.executable}\n" + r'''
import os,pathlib,shutil,sys
args=sys.argv[1:]
url=args[-1]
assert url.startswith('https://github.com/pagepop/nacos-cli/releases/')
if '--head' in args:
 print('https://github.com/pagepop/nacos-cli/releases/tag/v'+os.environ['TEST_RELEASE_VERSION'],end='')
else:
 shutil.copyfile(pathlib.Path(os.environ['TEST_RELEASE_DIR'])/url.rsplit('/',1)[-1],args[args.index('--output')+1])
''')
        curl.chmod(0o755)

    def run_installer(self, required=True):
        release_os = "darwin" if platform.system() == "Darwin" else "linux"
        release_arch = "arm64" if platform.machine() in ("arm64", "aarch64") else "amd64"
        archive_name = f"nacos-cli-{self.version}-{release_os}-{release_arch}.tar.gz"
        archive = self.root / archive_name
        executable = (f"#!{sys.executable}\nimport sys\n"
                      f"print({'nacos-cli version ' + self.binary_version + ' (commit: controlled, built: test)'!r}"
                      f" if sys.argv[1:]==['--version'] else {self.help_text!r})\n").encode()
        with tarfile.open(archive, "w:gz") as bundle:
            entry = tarfile.TarInfo("nacos-cli")
            entry.size, entry.mode = len(executable), 0o755
            bundle.addfile(entry, io.BytesIO(executable))
        digest = hashlib.sha256(archive.read_bytes()).hexdigest()
        (self.root / "checksums.txt").write_text(f"{digest}  {archive_name}\n")
        env = os.environ.copy()
        env.update({"PATH": str(self.harness) + os.pathsep + env["PATH"],
                    "TEST_RELEASE_DIR": str(self.root), "TEST_RELEASE_VERSION": self.version,
                    "LC_ALL": "C.UTF-8"})
        env.pop("NACOS_CLI_VERSION", None)
        env.pop("NACOS_CLI_INSTALL_DIR", None)
        args = ["bash", str(INSTALLER), "--install-dir", str(self.target.parent)]
        if required:
            args += ["--require-command", "role-list", "--require-option", "--all",
                     "--require-option", "--output"]
        return subprocess.run(args, cwd=self.project, env=env, capture_output=True, text=True, timeout=20)

    def test_compatible_candidate_is_installed(self):
        result = self.run_installer()
        self.assertEqual(result.returncode, 0, result.stderr)
        version = subprocess.check_output([str(self.target), "--version"], text=True)
        self.assertIn(self.version + " (", version)

    def test_missing_option_preserves_old_binary(self):
        self.help_text = self.help_text.replace("--all", "--all-extra")
        result = self.run_installer()
        self.assertNotEqual(result.returncode, 0)
        self.assertEqual(self.target.read_bytes(), self.old)

    def test_root_help_does_not_prove_command_support(self):
        self.help_text = self.help_text.replace("nacos-cli role-list", "nacos-cli")
        result = self.run_installer()
        self.assertNotEqual(result.returncode, 0)
        self.assertEqual(self.target.read_bytes(), self.old)

    def test_version_prefix_is_not_an_exact_version_match(self):
        self.binary_version = self.version + "0"
        result = self.run_installer()
        self.assertNotEqual(result.returncode, 0)
        self.assertEqual(self.target.read_bytes(), self.old)

    def test_existing_install_usage_remains_supported(self):
        result = self.run_installer(required=False)
        self.assertEqual(result.returncode, 0, result.stderr)

    def test_final_replacement_failure_preserves_old_binary_and_backup(self):
        real_mv = shutil.which("mv")
        self.assertIsNotNone(real_mv)
        fake_mv = self.harness / "mv"
        fake_mv.write_text(f"#!{sys.executable}\n" +
                           "import os,pathlib,sys\n"
                           "if any(pathlib.Path(arg).name.startswith('.nacos-cli.install.') "
                           "for arg in sys.argv[1:]): raise SystemExit(47)\n"
                           f"os.execv({real_mv!r}, [{real_mv!r}, *sys.argv[1:]])\n")
        fake_mv.chmod(0o755)
        result = self.run_installer()
        self.assertNotEqual(result.returncode, 0)
        self.assertTrue(self.target.is_file(), result.stderr)
        self.assertEqual(self.target.read_bytes(), self.old)
        backup = re.search(r"previous binary to (.+)", result.stderr)
        self.assertIsNotNone(backup, result.stderr)
        self.assertEqual(Path(backup.group(1)).read_bytes(), self.old)


if __name__ == "__main__":
    unittest.main()
