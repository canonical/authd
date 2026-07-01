import asyncio
import io
import os
from pathlib import Path
import subprocess
import sys
import unittest
from unittest.mock import call, patch

from robot.api import logger
from robot.api.deco import keyword, library
from robot.running import TestSuite

sys.path.insert(0, str(Path(__file__).resolve().parents[1] / 'resources'))

from Checkpoint import Checkpoint
from Snapshot import Snapshot
import VMUtils


class CheckpointTests(unittest.TestCase):
    def setUp(self):
        self.enterContext(patch.dict(os.environ, {
            'BROKER': 'authd-test',
            'RELEASE': 'noble',
            'E2E_USER': 'user@example.com',
        }, clear=True))
        self.builtin = self.enterContext(patch('Checkpoint.BuiltIn')).return_value
        self.builtin.get_variable_value.return_value = 'local-password'
        self.builtin.run_keyword.side_effect = self.run_keyword
        self.snapshots = set()
        self.base_identity = 'revision-one'
        self.failure = None
        self.checkpoint = Checkpoint()

    def run_keyword(self, keyword, *args):
        if keyword == self.failure:
            raise RuntimeError('setup failed')
        if keyword == 'Snapshot.Get Identity':
            return f'{args[0]}-{self.base_identity}'
        if keyword == 'Snapshot.Exists':
            return args[0] in self.snapshots
        if keyword == 'Snapshot.Create':
            self.snapshots.add(args[0])

    def setup_checkpoint(self, checkpoint=None):
        (checkpoint or self.checkpoint).setup_checkpoint('user', 'Reach User')

    def snapshot_name(self, base='authd-test-installed', setup='Reach User'):
        return self.checkpoint.get_snapshot_name('user', base, setup)

    def test_creates_once_and_restores_exact_checkpoint(self):
        self.setup_checkpoint()
        snapshot = self.snapshots.pop()
        self.snapshots.add(snapshot)
        self.assertEqual(self.builtin.run_keyword.call_args_list[2:], [
            call('Snapshot.Restore', 'authd-test-installed'),
            call('utils.Wait Until System Time Synced'),
            call('utils.Log Installed Package Information', 'authd-test-installed'),
            call('Journal.Start Receiving Journal'),
            call('VNCRecorder.Start Recording'),
            call('Reach User'),
            call('Journal.Stop Receiving Journal'),
            call('VNCRecorder.Stop Recording'),
            call('Snapshot.Create', snapshot),
            call('Journal.Start Receiving Journal'),
            call('VNCRecorder.Start Recording'),
        ])
        self.builtin.run_keyword.reset_mock()
        self.setup_checkpoint()
        self.assertIn(call('Snapshot.Restore', snapshot),
                      self.builtin.run_keyword.call_args_list)
        self.assertNotIn(call('Reach User'), self.builtin.run_keyword.call_args_list)
        self.assertNotIn(call('utils.Restore Snapshot', snapshot),
                         self.builtin.run_keyword.call_args_list)

    def test_override_selects_base_not_checkpoint(self):
        os.environ['E2E_TEST_SNAPSHOT'] = 'prepared'
        self.setup_checkpoint()
        self.assertIn(call('Snapshot.Restore', 'prepared'),
                      self.builtin.run_keyword.call_args_list)
        snapshot = next(iter(self.snapshots))
        self.builtin.run_keyword.reset_mock()
        self.setup_checkpoint()
        self.assertIn(call('Snapshot.Restore', snapshot),
                      self.builtin.run_keyword.call_args_list)
        self.assertNotIn(call('Snapshot.Restore', 'prepared'),
                         self.builtin.run_keyword.call_args_list)

    def test_stable_metadata_uses_base_name(self):
        base = 'authd-test-stable-installed'
        self.checkpoint.setup_checkpoint('stable-user', 'Reach User', base)
        self.builtin.run_keyword.reset_mock()
        self.checkpoint.setup_checkpoint('stable-user', 'Reach User', base)
        self.assertIn(call('utils.Log Installed Package Information', base),
                      self.builtin.run_keyword.call_args_list)

    def test_identity_changes_with_inputs(self):
        original = self.snapshot_name()
        self.assertEqual(original, self.snapshot_name())
        self.assertNotEqual(original, self.snapshot_name(base='stable'))
        self.assertNotEqual(original, self.snapshot_name(setup='Other Setup'))
        with patch.dict(os.environ, {'E2E_USER': 'other@example.com'}):
            self.assertNotEqual(original, self.snapshot_name())
        self.builtin.get_variable_value.return_value = 'other-password'
        self.assertNotEqual(original, self.snapshot_name())
        self.builtin.get_variable_value.return_value = 'local-password'
        self.base_identity = 'revision-two'
        self.assertNotEqual(original, self.snapshot_name())

    def test_all_creation_failures_block_only_current_run(self):
        keywords = [
            'Snapshot.Restore', 'utils.Wait Until System Time Synced',
            'utils.Log Installed Package Information',
            'Journal.Start Receiving Journal', 'VNCRecorder.Start Recording',
            'Reach User', 'Journal.Stop Receiving Journal',
            'VNCRecorder.Stop Recording', 'Snapshot.Create',
        ]
        for keyword in keywords:
            with self.subTest(keyword=keyword):
                checkpoint = Checkpoint()
                self.snapshots.clear()
                self.failure = keyword
                with self.assertRaisesRegex(RuntimeError, 'setup failed'):
                    self.setup_checkpoint(checkpoint)
                self.failure = None
                self.builtin.run_keyword.reset_mock()
                with self.assertRaisesRegex(RuntimeError, 'failed earlier'):
                    self.setup_checkpoint(checkpoint)
                self.assertNotIn(call('Reach User'),
                                 self.builtin.run_keyword.call_args_list)
                self.setup_checkpoint(Checkpoint())
                self.assertEqual(len(self.snapshots), 1)

    def test_deleted_checkpoint_is_recreated(self):
        self.setup_checkpoint()
        self.snapshots.clear()
        self.builtin.run_keyword.reset_mock()
        self.setup_checkpoint()
        self.assertIn(call('Reach User'), self.builtin.run_keyword.call_args_list)

    def test_failure_is_not_shared_with_another_vm(self):
        self.failure = 'Reach User'
        with self.assertRaises(RuntimeError):
            self.setup_checkpoint()
        self.failure = None
        os.environ['VM_NAME'] = 'other-vm'
        self.setup_checkpoint()
        self.assertEqual(len(self.snapshots), 1)


class SnapshotTests(unittest.TestCase):
    def setUp(self):
        self.enterContext(patch.dict(os.environ, {'VM_NAME': 'test-vm'}, clear=True))
        self.run = self.enterContext(patch('Snapshot.ExecUtils.run'))
        self.snapshot = Snapshot()

    def test_exists_matches_full_names_only(self):
        self.run.return_value.stdout = (
            'authd-checkpoint-user-from-stable\nother\n\n'
        )
        self.assertFalse(asyncio.run(self.snapshot.exists('authd-checkpoint-user')))
        self.assertTrue(asyncio.run(
            self.snapshot.exists('authd-checkpoint-user-from-stable')
        ))
        self.assertEqual(self.run.call_args.args[0],
                         ['virsh', 'snapshot-list', 'test-vm', '--name'])
        self.assertTrue(self.run.call_args.kwargs['check'])

    def test_list_errors_are_not_treated_as_missing_snapshots(self):
        self.run.side_effect = subprocess.CalledProcessError(1, 'virsh')
        with self.assertRaises(subprocess.CalledProcessError):
            asyncio.run(self.snapshot.exists('checkpoint'))

    def test_snapshot_files_are_unique_and_use_actual_disk_target(self):
        self.run.return_value.stdout = """
            <domain><devices>
              <disk device="cdrom"><source file="/images/seed.iso"/></disk>
              <disk device="disk">
                <source file="/images/current-overlay"/>
                <target dev="sda"/>
              </disk>
            </devices></domain>
        """
        commands = []
        for _ in range(2):
            asyncio.run(self.snapshot.create('checkpoint'))
            commands.append(self.run.call_args.args[0])
        disks, memories = [], []
        for command in commands:
            disk = command[command.index('--diskspec') + 1]
            memory = command[command.index('--memspec') + 1]
            self.assertTrue(disk.startswith('sda,file=/images/test-vm-checkpoint.'))
            self.assertTrue(disk.endswith('.qcow2,snapshot=external'))
            self.assertTrue(memory.startswith('/images/test-vm-checkpoint.'))
            self.assertTrue(memory.endswith('.mem,snapshot=external'))
            disks.append(disk)
            memories.append(memory)
        self.assertNotEqual(disks[0], disks[1])
        self.assertNotEqual(memories[0], memories[1])

    def test_missing_disk_fails_before_snapshot_creation(self):
        self.run.return_value.stdout = '<domain><devices/></domain>'
        with self.assertRaisesRegex(RuntimeError, 'disk source'):
            asyncio.run(self.snapshot.create('checkpoint'))
        self.assertEqual(self.run.call_count, 1)

    def test_identity_tracks_base_revision(self):
        self.run.return_value.stdout = '<domainsnapshot>revision-one</domainsnapshot>'
        first = asyncio.run(self.snapshot.get_identity('base'))
        self.assertEqual(first, asyncio.run(self.snapshot.get_identity('base')))
        self.run.return_value.stdout = '<domainsnapshot>revision-two</domainsnapshot>'
        self.assertNotEqual(first, asyncio.run(self.snapshot.get_identity('base')))
        self.assertEqual(self.run.call_args.args[0],
                         ['virsh', 'snapshot-dumpxml', 'test-vm', 'base'])
        self.assertTrue(self.run.call_args.kwargs['check'])

    def test_vm_name_override_and_default(self):
        self.assertEqual(VMUtils.vm_name(), 'test-vm')
        del os.environ['VM_NAME']
        os.environ['RELEASE'] = 'noble'
        self.assertEqual(VMUtils.vm_name(), 'e2e-runner-noble')
        del os.environ['RELEASE']
        with self.assertRaisesRegex(Exception, 'RELEASE'):
            VMUtils.vm_name()


@library
class RobotServices:
    @keyword
    def wait_until_system_time_synced(self):
        pass

    @keyword
    def log_installed_package_information(self, snapshot):
        logger.info(snapshot)

    @keyword
    def start_receiving_journal(self):
        pass

    @keyword
    def stop_receiving_journal(self):
        pass

    @keyword
    def start_recording(self):
        pass

    @keyword
    def stop_recording(self):
        pass

    @keyword
    def reach_user(self):
        pass

    @keyword
    def fail_setup(self):
        raise RuntimeError('setup failed')


class RobotIntegrationTests(unittest.TestCase):
    def setUp(self):
        self.enterContext(patch.dict(os.environ, {
            'BROKER': 'authd-test', 'VM_NAME': 'test-vm',
            'E2E_USER': 'user@example.com', 'E2E_TEST_SNAPSHOT': 'prepared',
        }, clear=True))
        self.run = self.enterContext(patch('Snapshot.ExecUtils.run'))
        self.run.side_effect = self.virsh
        self.snapshots = set()

    def virsh(self, command, **kwargs):
        output = ''
        if command[1] == 'snapshot-dumpxml':
            output = '<domainsnapshot>revision-one</domainsnapshot>'
        elif command[1] == 'snapshot-list':
            output = '\n'.join(sorted(self.snapshots))
        elif command[1] == 'snapshot-create-as':
            self.snapshots.add(command[3])
        elif command[1] == 'dumpxml':
            output = (
                '<domain><devices><disk device="disk">'
                '<source file="/images/base"/><target dev="vda"/>'
                '</disk></devices></domain>'
            )
        return subprocess.CompletedProcess(command, 0, stdout=output)

    def make_suite(self, setup_keyword):
        suite = TestSuite('Checkpoints')
        resources = Path(__file__).resolve().parents[1] / 'resources'
        for name in ['First', 'Dependent']:
            child = suite.suites.create(name)
            child.resource.imports.library(str(resources / 'Checkpoint.py'))
            child.resource.imports.library(str(resources / 'Snapshot.py'))
            for alias in ['utils', 'Journal', 'VNCRecorder']:
                child.resource.imports.library(
                    'test_checkpoints.RobotServices', alias=alias
                )
            child.resource.variables.create('${local_password}', ['password'])
            test = child.tests.create(name)
            test.setup.config(name='Checkpoint.Setup Checkpoint',
                              args=['user', setup_keyword])
            test.body.create_keyword('No Operation')
        return suite

    def test_robot_executes_dynamic_async_snapshot_keywords(self):
        result = self.make_suite('utils.Reach User').run(
            output=None, log=None, report=None, stdout=io.StringIO(),
            stderr=io.StringIO(),
        )
        self.assertEqual(result.statistics.total.passed, 2)
        self.assertEqual(len(self.snapshots), 1)
        restores = [
            args.args[0][3] for args in self.run.call_args_list
            if args.args[0][1] == 'snapshot-revert'
        ]
        self.assertEqual(restores, ['prepared', next(iter(self.snapshots))])

    def test_failed_dependency_remains_failed_and_new_run_retries(self):
        for _ in range(2):
            result = self.make_suite('utils.Fail Setup').run(
                output=None, log=None, report=None, stdout=io.StringIO(),
                stderr=io.StringIO(),
            )
            first, dependent = [suite.tests[0] for suite in result.suite.suites]
            self.assertEqual(first.status, 'FAIL')
            self.assertIn('setup failed', first.message)
            self.assertEqual(dependent.status, 'FAIL')
            self.assertIn('failed earlier in this run', dependent.message)


if __name__ == '__main__':
    unittest.main()
