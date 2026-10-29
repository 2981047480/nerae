"""Failure paths for the contract generation entry point."""
from pathlib import Path
import os
import tempfile
import unittest
from unittest import mock

import generate


class CodegenSafetyTests(unittest.TestCase):
    def setUp(self):
        self.temporary = tempfile.TemporaryDirectory(prefix='nerae-codegen-tests-')
        self.addCleanup(self.temporary.cleanup)
        self.root = Path(self.temporary.name).resolve()
        self.root_patch = mock.patch.object(generate, 'ROOT', self.root)
        self.root_patch.start()
        self.addCleanup(self.root_patch.stop)

    def write(self, relative, content):
        path = self.root / relative
        path.parent.mkdir(parents=True, exist_ok=True)
        path.write_text(content)
        return path

    def test_path_escape_and_symlink_are_rejected(self):
        with self.assertRaisesRegex(ValueError, 'unsafe path'):
            generate.local_file('../outside', must_exist=False)
        (self.root / 'alias').symlink_to('/tmp', target_is_directory=True)
        with self.assertRaisesRegex(ValueError, 'symlink'):
            generate.local_file('alias/file', must_exist=False)

    def test_escaped_npm_tool_is_rejected(self):
        directory = self.root / 'scripts/codegen/npm/node_modules/.bin'
        directory.mkdir(parents=True)
        (directory / 'openapi-typescript').symlink_to('/bin/echo')
        with self.assertRaisesRegex(ValueError, 'escapes'):
            generate.npm_bin('openapi-typescript')

    def test_output_replacement_rolls_back_if_second_write_fails(self):
        outputs = {'ts': Path('types/generated.ts'), 'go': Path('go/generated.go')}
        first = self.write(outputs['ts'], 'old ts')
        second = self.write(outputs['go'], 'old go')
        replace = os.replace
        calls = []
        def fail_second(source, destination):
            calls.append((source, destination))
            if len(calls) == 2:
                raise OSError('injected replacement failure')
            return replace(source, destination)
        with mock.patch.object(generate, 'OUTPUTS', outputs), mock.patch.object(generate.os, 'replace', fail_second):
            with self.assertRaisesRegex(OSError, 'injected'):
                generate.write_outputs({'ts': b'new ts', 'go': b'new go'})
        self.assertEqual(first.read_text(), 'old ts')
        self.assertEqual(second.read_text(), 'old go')

    def test_interrupted_write_blocks_check_and_is_recoverable_by_write(self):
        with generate.write_lock('write') as marker:
            marker.write_text('nerae-codegen-write-v1\n')
        with self.assertRaisesRegex(ValueError, 'interrupted'):
            with generate.write_lock('check'):
                pass
        with generate.write_lock('write') as marker:
            self.assertTrue(marker.is_file())
            marker.unlink()
        with generate.write_lock('check'):
            pass

    def test_marker_is_published_only_after_a_complete_fsync(self):
        with generate.write_lock('write') as marker:
            with mock.patch.object(generate.os, 'fsync', side_effect=OSError('injected fsync failure')):
                with self.assertRaisesRegex(OSError, 'injected'):
                    generate.create_pending_marker(marker)
            self.assertFalse(marker.exists())
            self.assertEqual(list(marker.parent.glob('.write.pending.*')), [])
        with generate.write_lock('check'):
            pass

    def test_interrupted_marker_publication_can_be_retried(self):
        with generate.write_lock('write') as marker:
            # A killed process can leave a partial temporary file, but no output
            # was replaced before the pending marker was atomically published.
            temporary = marker.parent / '.write.pending.interrupted'
            temporary.write_text('nera')
        with generate.write_lock('check'):
            pass
        with generate.write_lock('write') as marker:
            generate.create_pending_marker(marker)
        with self.assertRaisesRegex(ValueError, 'interrupted'):
            with generate.write_lock('check'):
                pass
        with generate.write_lock('write') as marker:
            marker.unlink()
        with generate.write_lock('check'):
            pass

    def test_unknown_pending_marker_is_rejected_for_write_and_check(self):
        for content in ('', 'nerae-codegen-write-v1'):
            with self.subTest(content=content):
                marker = self.write('.review/nerae-codegen/write.pending', content)
                for mode in ('check', 'write'):
                    with self.assertRaisesRegex(ValueError, 'unknown pending'):
                        with generate.write_lock(mode):
                            pass
                marker.unlink()

    def test_simultaneous_operations_do_not_acquire_same_lock(self):
        with generate.write_lock('check'):
            with self.assertRaisesRegex(ValueError, 'another codegen'):
                with generate.write_lock('write'):
                    pass

    def test_external_refs_and_unreviewed_output_are_rejected(self):
        self.write('api/openapi.yaml', 'components:\n  schemas:\n    Bad:\n      $ref: "https://example.invalid/schema"\n')
        self.write('api/oapi-codegen.yaml', 'output: apps/api/internal/gen/api.gen.go\noutput-options:\n  nullable-type: true\n  user-templates:\n    imports.tmpl: api/templates/oapi-imports.tmpl\n')
        with self.assertRaisesRegex(AssertionError, 'only local'):
            generate.validate_inputs()
        self.write('api/openapi.yaml', 'components: {}\n')
        self.write('api/oapi-codegen.yaml', 'output: ../../outside.go\n')
        with self.assertRaisesRegex(ValueError, 'designated generated file'):
            generate.validate_inputs()


if __name__ == '__main__':
    unittest.main()
