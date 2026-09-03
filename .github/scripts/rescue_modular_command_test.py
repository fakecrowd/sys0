import pathlib
import unittest

ROOT = pathlib.Path(__file__).resolve().parents[2]
SOURCE = ROOT / "sys0-rescue" / "src" / "main.zig"


class RescueModularCommandSourceTests(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.source = SOURCE.read_text(encoding="utf-8")

    def test_modular_commands_use_per_module_queue(self):
        self.assertIn("queueAndKillModules(io, cfg.modules, .update)", self.source)
        self.assertIn("queueAndKillModules(io, cfg.modules, .restart)", self.source)

    def test_module_supervisor_waits_for_child_and_peer_to_stop(self):
        self.assertIn("pending_command != .none and (child_alive or st.online)", self.source)
        self.assertIn("g_mod_commands.complete(sup.idx, pending_command)", self.source)

    def test_update_downloads_before_replacing_canonical(self):
        start = self.source.index("fn superviseModule(")
        end = self.source.index("fn reapModule(", start)
        supervisor = self.source[start:end]
        self.assertNotIn("deleteFile(io, bin_path)", supervisor)
        self.assertIn("installModule(gpa, io, cfg, mod, bin_path, pending_command == .update)", supervisor)

    def test_download_is_validated_before_atomic_publish(self):
        start = self.source.index("fn downloadModule(")
        end = self.source.index("fn installModule(", start)
        download = self.source[start:end]
        validate_at = download.index("agentLooksValid(io, tmp_path)")
        publish_at = download.index("rename(tmp_path, cwd, dest_path, io)")
        self.assertLess(validate_at, publish_at)

    def test_spawn_publication_is_atomic_with_command_queueing(self):
        start = self.source.index("fn superviseModule(")
        end = self.source.index("fn reapModule(", start)
        supervisor = self.source[start:end]
        lock_at = supervisor.index("// Serialize the final command check, spawn, and child publication")
        spawn_at = supervisor.index("std.process.spawn", lock_at)
        pointer_at = supervisor.index("g_mod_children[sup.idx] = cptr", spawn_at)
        complete_at = supervisor.index("g_mod_commands.complete(sup.idx, pending_command)", spawn_at)
        unlock_at = supervisor.index("g_child_mu.unlock(io)", pointer_at)
        self.assertLess(lock_at, spawn_at)
        self.assertLess(spawn_at, pointer_at)
        self.assertLess(pointer_at, complete_at)
        self.assertLess(complete_at, unlock_at)

    def test_module_child_pointer_is_cleared_on_every_reaper_exit(self):
        start = self.source.index("fn reapModule(")
        end = self.source.index("fn prepareModuleDecoy", start)
        reaper = self.source[start:end]
        self.assertGreaterEqual(reaper.count("g_mod_children[idx] = null"), 2)
        self.assertGreaterEqual(reaper.count("g_mod_child_alive[idx] = false"), 2)


if __name__ == "__main__":
    unittest.main()
