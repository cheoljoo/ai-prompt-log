import json
import shutil
import tempfile
import unittest
from pathlib import Path

from apl import backup, model, source


class TestFormatDetection(unittest.TestCase):
    def setUp(self):
        self.tmpdir = Path(tempfile.mkdtemp())

    def tearDown(self):
        shutil.rmtree(self.tmpdir)

    def test_detect_claude(self):
        f = self.tmpdir / "claude.jsonl"
        f.write_text(
            json.dumps({"type": "user", "message": {"content": "hello world"}, "sessionId": "s1"}) + "\n"
        )
        self.assertEqual(model.detect_file_format(f), "claude")

    def test_detect_agy(self):
        f = self.tmpdir / "agy.jsonl"
        f.write_text(
            json.dumps({"step_index": 0, "type": "USER_INPUT", "source": "USER_EXPLICIT", "content": "<USER_REQUEST>hi</USER_REQUEST>"}) + "\n"
        )
        self.assertEqual(model.detect_file_format(f), "agy")

    def test_detect_agy_metadata(self):
        f = self.tmpdir / "agy_backed_up.jsonl"
        f.write_text(
            json.dumps({"type": "agy_metadata", "cwd": "/test/path", "sessionId": "abc"}) + "\n"
        )
        self.assertEqual(model.detect_file_format(f), "agy")

    def test_detect_gemini_json(self):
        f = self.tmpdir / "gemini.json"
        f.write_text(
            json.dumps({"sessionId": "g1", "messages": [{"type": "user", "content": [{"text": "hi"}]}]})
        )
        self.assertEqual(model.detect_file_format(f), "gemini_json")

    def test_detect_gemini_jsonl(self):
        f = self.tmpdir / "gemini.jsonl"
        f.write_text(
            json.dumps({"type": "gemini_metadata", "cwd": "/test/path"}) + "\n"
        )
        self.assertEqual(model.detect_file_format(f), "gemini_jsonl")


class TestParsers(unittest.TestCase):
    def setUp(self):
        self.tmpdir = Path(tempfile.mkdtemp())

    def tearDown(self):
        shutil.rmtree(self.tmpdir)

    def test_parse_claude(self):
        f = self.tmpdir / "claude.jsonl"
        lines = [
            json.dumps({"type": "user", "sessionId": "c1", "timestamp": "2026-09-01T10:00:00Z", "gitBranch": "main", "message": {"content": "build feature X"}}),
            json.dumps({"type": "assistant", "message": {"content": [{"type": "tool_use", "name": "Bash", "input": {"command": "ls"}}, {"type": "text", "text": "Feature done"}], "usage": {"input_tokens": 100, "output_tokens": 50}}}),
        ]
        f.write_text("\n".join(lines) + "\n")
        prompts = model.parse_claude_session_file(f)
        self.assertEqual(len(prompts), 1)
        p = prompts[0]
        self.assertEqual(p.source, "claude")
        self.assertEqual(p.user_text, "build feature X")
        self.assertEqual(p.branch, "main")
        self.assertEqual(p.total_tokens, 150)
        self.assertEqual(len(p.blocks), 2)
        self.assertEqual(p.blocks[0].kind, "tool_use")
        self.assertEqual(p.blocks[0].tool_name, "Bash")
        self.assertEqual(p.blocks[1].kind, "text")
        self.assertEqual(p.blocks[1].text, "Feature done")

    def test_parse_agy(self):
        f = self.tmpdir / "agy.jsonl"
        lines = [
            json.dumps({
                "step_index": 0,
                "source": "USER_EXPLICIT",
                "type": "USER_INPUT",
                "created_at": "2026-09-19T04:46:48Z",
                "content": "<USER_REQUEST>\ncreate branch 260919-agy\n</USER_REQUEST>\n<ADDITIONAL_METADATA>\ntime=now\n</ADDITIONAL_METADATA>"
            }),
            json.dumps({
                "step_index": 1,
                "source": "MODEL",
                "type": "PLANNER_RESPONSE",
                "tool_calls": [{"name": "run_command", "args": {"CommandLine": "\"git status\""}}]
            }),
            json.dumps({
                "step_index": 2,
                "source": "MODEL",
                "type": "PLANNER_RESPONSE",
                "content": "All done successfully."
            }),
        ]
        f.write_text("\n".join(lines) + "\n")
        prompts = model.parse_agy_transcript_file(f, session_id="test-conv", default_branch="260919-agy")
        self.assertEqual(len(prompts), 1)
        p = prompts[0]
        self.assertEqual(p.source, "agy")
        self.assertEqual(p.user_text, "create branch 260919-agy")
        self.assertEqual(p.branch, "260919-agy")
        self.assertEqual(p.session_id, "test-conv")
        self.assertEqual(len(p.blocks), 2)
        self.assertEqual(p.blocks[0].kind, "tool_use")
        self.assertEqual(p.blocks[0].tool_name, "run_command")
        self.assertEqual(p.blocks[0].tool_input["CommandLine"], "git status")
        self.assertEqual(p.blocks[1].kind, "text")
        self.assertEqual(p.blocks[1].text, "All done successfully.")

    def test_parse_gemini_json(self):
        f = self.tmpdir / "session.json"
        data = {
            "sessionId": "gemini-1",
            "messages": [
                {"type": "user", "timestamp": "2026-03-31T04:19:17Z", "content": [{"text": "explain w3m"}]},
                {"type": "gemini", "content": "w3m is a terminal web browser.", "tokens": {"total": 500}}
            ]
        }
        f.write_text(json.dumps(data))
        prompts = model.parse_gemini_json_file(f)
        self.assertEqual(len(prompts), 1)
        p = prompts[0]
        self.assertEqual(p.source, "gemini")
        self.assertEqual(p.user_text, "explain w3m")
        self.assertEqual(p.total_tokens, 500)
        self.assertEqual(len(p.blocks), 1)
        self.assertEqual(p.blocks[0].text, "w3m is a terminal web browser.")


class TestBackupAndIntegration(unittest.TestCase):
    def setUp(self):
        self.tmpdir = Path(tempfile.mkdtemp())
        self.backup_dir = self.tmpdir / "backup"

    def tearDown(self):
        shutil.rmtree(self.tmpdir)

    def test_backup_and_view(self):
        # Create a mock project
        ws_path = Path("/data01/cheoljoo.lee/code/test-proj")
        encoded_name = source.encode_path(ws_path)
        
        agy_f = self.tmpdir / "agy_transcript.jsonl"
        agy_f.write_text(
            json.dumps({"step_index": 0, "type": "USER_INPUT", "source": "USER_EXPLICIT", "content": "<USER_REQUEST>agy prompt</USER_REQUEST>"}) + "\n"
        )
        
        claude_f = self.tmpdir / "claude_session.jsonl"
        claude_f.write_text(
            json.dumps({"type": "user", "sessionId": "c1", "message": {"content": "claude prompt"}}) + "\n"
        )

        proj = model.Project(
            dir_path=ws_path,
            display_name="test-proj",
            cwd=str(ws_path),
            session_files=[claude_f, agy_f],
            conv_metadata={"agy_transcript": {"branch": "main", "id": "agy-123"}},
        )

        copied, updated, unchanged = backup.copy_project(proj, self.backup_dir)
        self.assertEqual(copied, 2)
        self.assertEqual(updated, 0)
        self.assertEqual(unchanged, 0)

        # Re-copy: should be unchanged
        copied2, updated2, unchanged2 = backup.copy_project(proj, self.backup_dir)
        self.assertEqual(copied2, 0)
        self.assertEqual(unchanged2, 2)

        # Verify load_projects on backup dir
        loaded_projs = model.load_projects(self.backup_dir)
        self.assertEqual(len(loaded_projs), 1)
        lp = loaded_projs[0]
        self.assertEqual(lp.display_name, "test-proj")
        self.assertEqual(lp.cwd, str(ws_path))
        loaded_prompts = lp.load_prompts()
        self.assertEqual(len(loaded_prompts), 2)
        sources = {p.source for p in loaded_prompts}
        self.assertEqual(sources, {"claude", "agy"})


if __name__ == "__main__":
    unittest.main()
