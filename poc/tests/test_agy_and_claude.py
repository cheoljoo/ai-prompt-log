import json
import shutil
import sqlite3
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

    def test_detect_opencode_metadata(self):
        f = self.tmpdir / "opencode.jsonl"
        f.write_text(
            json.dumps({"type": "opencode_metadata", "cwd": "/test/path", "sessionId": "ses_1"}) + "\n"
        )
        self.assertEqual(model.detect_file_format(f), "opencode")

    def test_detect_copilot_metadata(self):
        f = self.tmpdir / "copilot_backed_up.jsonl"
        f.write_text(
            json.dumps({"type": "copilot_metadata", "cwd": "/test/path", "sessionId": "abc"}) + "\n"
        )
        self.assertEqual(model.detect_file_format(f), "copilot")

    def test_detect_copilot_events(self):
        f = self.tmpdir / "events.jsonl"
        f.write_text(
            json.dumps({"type": "session.start", "data": {"context": {"cwd": "/test/path"}}}) + "\n"
        )
        self.assertEqual(model.detect_file_format(f), "copilot")


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

    def test_parse_opencode(self):
        f = self.tmpdir / "opencode.jsonl"
        lines = [
            json.dumps({"type": "opencode_metadata", "cwd": "/test/path", "sessionId": "ses_1"}),
            json.dumps({
                "type": "user",
                "sessionId": "ses_1",
                "timestamp": "2026-09-23T04:00:00+00:00",
                "gitBranch": "",
                "isSidechain": False,
                "message": {"content": "add opencode support"},
            }),
            json.dumps({
                "type": "assistant",
                "sessionId": "ses_1",
                "timestamp": "2026-09-23T04:00:01+00:00",
                "gitBranch": "",
                "isSidechain": False,
                "message": {
                    "content": [
                        {"type": "tool_use", "name": "bash", "input": {"command": "ls"}},
                        {"type": "text", "text": "Done."},
                    ],
                    "usage": {
                        "input_tokens": 10,
                        "output_tokens": 5,
                        "cache_read_input_tokens": 0,
                        "cache_creation_input_tokens": 0,
                    },
                },
            }),
        ]
        f.write_text("\n".join(lines) + "\n")
        prompts = model.parse_opencode_session_file(f)
        self.assertEqual(len(prompts), 1)
        p = prompts[0]
        self.assertEqual(p.source, "opencode")
        self.assertEqual(p.user_text, "add opencode support")
        self.assertEqual(p.total_tokens, 15)
        self.assertEqual(len(p.blocks), 2)
        self.assertEqual(p.blocks[0].kind, "tool_use")
        self.assertEqual(p.blocks[0].tool_name, "bash")
        self.assertEqual(p.blocks[1].kind, "text")
        self.assertEqual(p.blocks[1].text, "Done.")

    def test_parse_copilot(self):
        f = self.tmpdir / "events.jsonl"
        lines = [
            json.dumps({
                "type": "session.start",
                "timestamp": "2026-09-28T00:00:00Z",
                "data": {"context": {"cwd": "/test/path", "gitRoot": "/test/path", "branch": "main"}},
            }),
            json.dumps({
                "type": "user.message",
                "timestamp": "2026-09-28T00:00:01Z",
                "data": {"content": "add copilot support"},
            }),
            json.dumps({
                "type": "assistant.message",
                "timestamp": "2026-09-28T00:00:02Z",
                "data": {
                    "content": "Done.",
                    "toolRequests": [
                        {"toolCallId": "1", "name": "create", "arguments": {"path": "/a/new.py", "content": "x"}},
                    ],
                },
            }),
        ]
        f.write_text("\n".join(lines) + "\n")
        prompts = model.parse_copilot_events_file(f, session_id="conv-1", default_branch="main")
        self.assertEqual(len(prompts), 1)
        p = prompts[0]
        self.assertEqual(p.source, "copilot")
        self.assertEqual(p.user_text, "add copilot support")
        self.assertEqual(p.branch, "main")
        self.assertEqual(p.session_id, "conv-1")
        self.assertEqual(len(p.blocks), 2)
        self.assertEqual(p.blocks[0].kind, "text")
        self.assertEqual(p.blocks[0].text, "Done.")
        self.assertEqual(p.blocks[1].kind, "tool_use")
        self.assertEqual(p.blocks[1].tool_name, "create")
        self.assertEqual(p.blocks[1].tool_input["path"], "/a/new.py")
        self.assertEqual(p.timestamp, "2026-09-28T00:00:01Z")
        self.assertEqual(p.end_timestamp, "2026-09-28T00:00:02Z")
        self.assertEqual(p.final_result, "Done.")

    def test_parse_copilot_session_id_fallback(self):
        session_dir = self.tmpdir / "0928abcd-uuid"
        session_dir.mkdir()
        f = session_dir / "events.jsonl"
        f.write_text(
            json.dumps({"type": "user.message", "timestamp": "2026-09-28T00:00:00Z", "data": {"content": "hi"}}) + "\n"
        )
        prompts = model.parse_copilot_events_file(f)
        self.assertEqual(len(prompts), 1)
        self.assertEqual(prompts[0].session_id, "0928abcd-uuid")

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


class TestOpencodeSource(unittest.TestCase):
    def setUp(self):
        self.tmpdir = Path(tempfile.mkdtemp())
        self.db_path = self.tmpdir / "opencode.db"
        self.cache_dir = self.tmpdir / "cache"
        conn = sqlite3.connect(self.db_path)
        conn.execute(
            "CREATE TABLE session (id text PRIMARY KEY, project_id text, "
            "parent_id text, directory text, title text, time_created integer, "
            "time_updated integer)"
        )
        conn.execute(
            "CREATE TABLE message (id text PRIMARY KEY, session_id text, "
            "time_created integer, time_updated integer, data text)"
        )
        conn.execute(
            "CREATE TABLE part (id text PRIMARY KEY, message_id text, "
            "session_id text, time_created integer, time_updated integer, data text)"
        )
        conn.execute(
            "INSERT INTO session VALUES (?, ?, ?, ?, ?, ?, ?)",
            ("ses_1", "proj_1", None, "/data01/test-proj", "test session", 1000, 2000),
        )
        conn.execute(
            "INSERT INTO message VALUES (?, ?, ?, ?, ?)",
            (
                "msg_1",
                "ses_1",
                1000,
                1000,
                json.dumps({"role": "user", "time": {"created": 1000}}),
            ),
        )
        conn.execute(
            "INSERT INTO part VALUES (?, ?, ?, ?, ?, ?)",
            (
                "prt_1",
                "msg_1",
                "ses_1",
                1000,
                1000,
                json.dumps({"type": "text", "text": "hello opencode"}),
            ),
        )
        conn.execute(
            "INSERT INTO message VALUES (?, ?, ?, ?, ?)",
            (
                "msg_2",
                "ses_1",
                1500,
                1500,
                json.dumps({"role": "assistant", "time": {"created": 1500, "completed": 1600}}),
            ),
        )
        conn.execute(
            "INSERT INTO part VALUES (?, ?, ?, ?, ?, ?)",
            (
                "prt_2",
                "msg_2",
                "ses_1",
                1500,
                1500,
                json.dumps({"type": "text", "text": "hi there"}),
            ),
        )
        conn.execute(
            "INSERT INTO part VALUES (?, ?, ?, ?, ?, ?)",
            (
                "prt_3",
                "msg_2",
                "ses_1",
                1600,
                1600,
                json.dumps({
                    "type": "step-finish",
                    "tokens": {"input": 20, "output": 10, "cache": {"read": 0, "write": 0}},
                }),
            ),
        )
        conn.commit()
        conn.close()

    def tearDown(self):
        shutil.rmtree(self.tmpdir)

    def test_scan_and_parse(self):
        convs = source.scan_opencode_conversations(
            db_path=self.db_path, cache_dir=self.cache_dir
        )
        self.assertEqual(len(convs), 1)
        c = convs[0]
        self.assertEqual(c["id"], "ses_1")
        self.assertEqual(c["workspace"], "/data01/test-proj")
        self.assertTrue(c["transcript"].exists())
        self.assertEqual(model.detect_file_format(c["transcript"]), "opencode")

        prompts = model.parse_session_file(c["transcript"])
        self.assertEqual(len(prompts), 1)
        p = prompts[0]
        self.assertEqual(p.source, "opencode")
        self.assertEqual(p.user_text, "hello opencode")
        self.assertEqual(p.total_tokens, 30)
        self.assertEqual(len(p.blocks), 1)
        self.assertEqual(p.blocks[0].text, "hi there")

    def test_cache_regenerated_when_stale(self):
        convs = source.scan_opencode_conversations(
            db_path=self.db_path, cache_dir=self.cache_dir
        )
        cache_file = convs[0]["transcript"]
        first_mtime = cache_file.stat().st_mtime

        # Bump the session's time_updated and add a new message -> should regen.
        conn = sqlite3.connect(self.db_path)
        conn.execute("UPDATE session SET time_updated=? WHERE id='ses_1'", (999_999_999_999,))
        conn.commit()
        conn.close()

        convs2 = source.scan_opencode_conversations(
            db_path=self.db_path, cache_dir=self.cache_dir
        )
        self.assertGreaterEqual(convs2[0]["transcript"].stat().st_mtime, first_mtime)


class TestParseTimestampLoose(unittest.TestCase):
    def test_valid_formats(self):
        self.assertIsNotNone(model.parse_timestamp_loose("2026-09-28T03:55:49.761Z"))
        self.assertIsNotNone(model.parse_timestamp_loose("2026-09-28T03:55:49Z"))
        self.assertIsNotNone(model.parse_timestamp_loose("2026-09-28T03:55:49.761+0900"))
        self.assertIsNotNone(model.parse_timestamp_loose("2026-09-28T03:55:49"))

    def test_invalid_formats(self):
        self.assertIsNone(model.parse_timestamp_loose(""))
        self.assertIsNone(model.parse_timestamp_loose("not-a-timestamp"))


class TestBuildSaveDocument(unittest.TestCase):
    def setUp(self):
        self.tmpdir = Path(tempfile.mkdtemp())
        self.addCleanup(shutil.rmtree, self.tmpdir, ignore_errors=True)

    def _make_project(self):
        f = self.tmpdir / "events.jsonl"
        lines = [
            json.dumps({
                "type": "session.start",
                "timestamp": "2020-01-01T00:00:00Z",
                "data": {"context": {"cwd": "/test/path", "gitRoot": "/test/path", "branch": "main"}},
            }),
            json.dumps({
                "type": "user.message",
                "timestamp": "2020-01-01T00:00:01Z",
                "data": {"content": "old prompt"},
            }),
            json.dumps({
                "type": "assistant.message",
                "timestamp": "2020-01-01T00:00:02Z",
                "data": {"content": "old result", "toolRequests": []},
            }),
            json.dumps({
                "type": "user.message",
                "timestamp": "2026-01-01T00:00:01Z",
                "data": {"content": "new prompt"},
            }),
            json.dumps({
                "type": "assistant.message",
                "timestamp": "2026-01-01T00:00:02Z",
                "data": {
                    "content": "new result",
                    "toolRequests": [
                        {"toolCallId": "1", "name": "create", "arguments": {"path": "/a/new.py", "content": "x"}},
                    ],
                },
            }),
        ]
        f.write_text("\n".join(lines) + "\n")
        return model.Project(
            dir_path=self.tmpdir,
            display_name="test-proj",
            cwd="/test/path",
            source="copilot",
            session_files=[f],
        )

    def test_no_filter_keeps_both(self):
        proj = self._make_project()
        doc = model.build_save_document([proj], "copilot")
        self.assertEqual(doc["source_filter"], "copilot")
        self.assertNotIn("since", doc)
        self.assertEqual(len(doc["projects"]), 1)
        prompts = doc["projects"][0]["prompts"]
        self.assertEqual(doc["projects"][0]["prompt_count"], 2)
        self.assertEqual(prompts[0]["user_prompt"], "old prompt")
        self.assertEqual(prompts[0]["final_result"], "old result")
        self.assertEqual(prompts[0]["start_time"], "2020-01-01T00:00:01Z")
        self.assertEqual(prompts[0]["end_time"], "2020-01-01T00:00:02Z")
        self.assertEqual(prompts[1]["user_prompt"], "new prompt")
        self.assertEqual(prompts[1]["final_result"], "new result")
        self.assertEqual(prompts[1]["modified_files"], [{"path": "/a/new.py", "action": "created"}])

    def test_since_cutoff_filters_old_prompt(self):
        from datetime import datetime

        proj = self._make_project()
        since = datetime(2025, 1, 1)
        doc = model.build_save_document([proj], "copilot", since=since)
        self.assertIn("since", doc)
        prompts = doc["projects"][0]["prompts"]
        self.assertEqual(len(prompts), 1)
        self.assertEqual(prompts[0]["user_prompt"], "new prompt")


class TestFileChanges(unittest.TestCase):
    def _prompt(self, blocks):
        return model.Prompt(
            session_id="s1",
            timestamp="2026-09-23T00:00:00Z",
            branch="",
            sidechain=False,
            user_text="do stuff",
            blocks=blocks,
        )

    def test_claude_write_and_edit(self):
        blocks = [
            model.AssistantBlock("tool_use", tool_name="Write", tool_input={"file_path": "/a/new.py", "content": "x"}),
            model.AssistantBlock("tool_use", tool_name="Edit", tool_input={"file_path": "/a/existing.py", "old_string": "x", "new_string": "y"}),
        ]
        changes = self._prompt(blocks).file_changes
        self.assertEqual(changes, [("/a/new.py", "created"), ("/a/existing.py", "modified")])

    def test_claude_edit_after_write_stays_created(self):
        blocks = [
            model.AssistantBlock("tool_use", tool_name="Write", tool_input={"file_path": "/a/new.py", "content": "x"}),
            model.AssistantBlock("tool_use", tool_name="Edit", tool_input={"file_path": "/a/new.py", "old_string": "x", "new_string": "y"}),
        ]
        changes = self._prompt(blocks).file_changes
        self.assertEqual(changes, [("/a/new.py", "created")])

    def test_claude_bash_rm(self):
        blocks = [
            model.AssistantBlock("tool_use", tool_name="Bash", tool_input={"command": "rm -rf /tmp/foo /tmp/bar"}),
        ]
        changes = self._prompt(blocks).file_changes
        self.assertEqual(changes, [("/tmp/foo", "deleted"), ("/tmp/bar", "deleted")])

    def test_claude_bash_mv(self):
        blocks = [
            model.AssistantBlock("tool_use", tool_name="Bash", tool_input={"command": "mv /tmp/old.txt /tmp/new.txt"}),
        ]
        changes = self._prompt(blocks).file_changes
        self.assertEqual(changes, [("/tmp/old.txt", "deleted"), ("/tmp/new.txt", "created")])

    def test_agy_write_and_replace(self):
        blocks = [
            model.AssistantBlock("tool_use", tool_name="write_to_file", tool_input={"TargetFile": "/a/x.py", "Overwrite": True, "CodeContent": "x"}),
            model.AssistantBlock("tool_use", tool_name="replace_file_content", tool_input={"TargetFile": "/a/y.py"}),
            model.AssistantBlock("tool_use", tool_name="run_command", tool_input={"CommandLine": "rm -f /tmp/z.txt"}),
        ]
        changes = self._prompt(blocks).file_changes
        self.assertEqual(
            changes,
            [("/a/x.py", "created"), ("/a/y.py", "modified"), ("/tmp/z.txt", "deleted")],
        )

    def test_opencode_edit_create_vs_modify(self):
        blocks = [
            model.AssistantBlock("tool_use", tool_name="edit", tool_input={"filePath": "/a/new.py", "newString": "x"}),
            model.AssistantBlock("tool_use", tool_name="edit", tool_input={"filePath": "/a/old.py", "oldString": "x", "newString": "y"}),
            model.AssistantBlock("tool_use", tool_name="bash", tool_input={"command": "rm /tmp/gone.py"}),
        ]
        changes = self._prompt(blocks).file_changes
        self.assertEqual(
            changes,
            [("/a/new.py", "created"), ("/a/old.py", "modified"), ("/tmp/gone.py", "deleted")],
        )

    def test_copilot_create_and_edit(self):
        blocks = [
            model.AssistantBlock("tool_use", tool_name="create", tool_input={"path": "/a/new.py", "content": "x"}),
            model.AssistantBlock("tool_use", tool_name="edit", tool_input={"path": "/a/existing.py"}),
        ]
        changes = self._prompt(blocks).file_changes
        self.assertEqual(changes, [("/a/new.py", "created"), ("/a/existing.py", "modified")])

    def test_gemini_write_file(self):
        blocks = [
            model.AssistantBlock("tool_use", tool_name="write_file", tool_input={"file_path": "out.py", "content": "x"}),
        ]
        changes = self._prompt(blocks).file_changes
        self.assertEqual(changes, [("out.py", "created")])

    def test_no_file_tools(self):
        blocks = [
            model.AssistantBlock("text", text="just talking"),
            model.AssistantBlock("tool_use", tool_name="Read", tool_input={"file_path": "/a/x.py"}),
        ]
        self.assertEqual(self._prompt(blocks).file_changes, [])


class TestCopilotSource(unittest.TestCase):
    def setUp(self):
        self.tmpdir = Path(tempfile.mkdtemp())

    def tearDown(self):
        shutil.rmtree(self.tmpdir)

    def test_scan_and_parse(self):
        session_dir = self.tmpdir / "sess-uuid-1"
        session_dir.mkdir()
        (session_dir / "workspace.yaml").write_text(
            "id: sess-uuid-1\n"
            "cwd: /data01/test-proj\n"
            "git_root: /data01/test-proj\n"
            "branch: main\n"
            "name: my session\n"
        )
        (session_dir / "events.jsonl").write_text(
            json.dumps({
                "type": "user.message",
                "timestamp": "2026-09-28T00:00:00Z",
                "data": {"content": "hello copilot"},
            }) + "\n"
        )

        # A session dir missing events.jsonl should be skipped.
        empty_dir = self.tmpdir / "sess-empty"
        empty_dir.mkdir()

        convs = source.scan_copilot_sessions(base=self.tmpdir)
        self.assertEqual(len(convs), 1)
        c = convs[0]
        self.assertEqual(c["id"], "sess-uuid-1")
        self.assertEqual(c["workspace"], "/data01/test-proj")
        self.assertEqual(c["branch"], "main")
        self.assertEqual(c["title"], "my session")
        self.assertEqual(model.detect_file_format(c["transcript"]), "copilot")

        prompts = model.parse_session_file(c["transcript"], session_id=c["id"], default_branch=c["branch"])
        self.assertEqual(len(prompts), 1)
        p = prompts[0]
        self.assertEqual(p.source, "copilot")
        self.assertEqual(p.user_text, "hello copilot")
        self.assertEqual(p.branch, "main")


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

        opencode_f = self.tmpdir / "opencode_session.jsonl"
        opencode_f.write_text(
            json.dumps({"type": "opencode_metadata", "cwd": str(ws_path), "sessionId": "ses_1"}) + "\n"
            + json.dumps({
                "type": "user",
                "sessionId": "ses_1",
                "message": {"content": "opencode prompt"},
            }) + "\n"
        )

        copilot_session_dir = self.tmpdir / "copilot-uuid-1"
        copilot_session_dir.mkdir()
        copilot_f = copilot_session_dir / "events.jsonl"
        copilot_f.write_text(
            json.dumps({
                "type": "user.message",
                "timestamp": "2026-09-28T00:00:00Z",
                "data": {"content": "copilot prompt"},
            }) + "\n"
        )

        proj = model.Project(
            dir_path=ws_path,
            display_name="test-proj",
            cwd=str(ws_path),
            session_files=[claude_f, agy_f, opencode_f, copilot_f],
            conv_metadata={
                "agy_transcript": {"branch": "main", "id": "agy-123"},
                "opencode_session": {"id": "ses_1"},
                "copilot-uuid-1": {"branch": "main", "id": "copilot-uuid-1"},
            },
        )

        copied, updated, unchanged = backup.copy_project(proj, self.backup_dir)
        self.assertEqual(copied, 4)
        self.assertEqual(updated, 0)
        self.assertEqual(unchanged, 0)

        # Re-copy: should be unchanged
        copied2, updated2, unchanged2 = backup.copy_project(proj, self.backup_dir)
        self.assertEqual(copied2, 0)
        self.assertEqual(unchanged2, 4)

        # Verify load_projects on backup dir
        loaded_projs = model.load_projects(self.backup_dir)
        self.assertEqual(len(loaded_projs), 1)
        lp = loaded_projs[0]
        self.assertEqual(lp.display_name, "test-proj")
        self.assertEqual(lp.cwd, str(ws_path))
        loaded_prompts = lp.load_prompts()
        self.assertEqual(len(loaded_prompts), 4)
        sources = {p.source for p in loaded_prompts}
        self.assertEqual(sources, {"claude", "agy", "opencode", "copilot"})


if __name__ == "__main__":
    unittest.main()
