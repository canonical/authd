import os
from pathlib import Path
import select
import subprocess
import tempfile
import unittest


class VMLockTests(unittest.TestCase):
    def setUp(self):
        self.runtime = self.enterContext(tempfile.TemporaryDirectory())
        self.library = Path(__file__).resolve().parents[1] / 'vm/lib/libprovision.sh'

    def start_runner(self, vm):
        process = subprocess.Popen(
            ['bash', '-euc', 'source "$1"; lock_vm; echo acquired; read -r release',
             'test', str(self.library)],
            env={**os.environ, 'XDG_RUNTIME_DIR': self.runtime, 'VM_NAME': vm},
            stdin=subprocess.PIPE, stdout=subprocess.PIPE, stderr=subprocess.PIPE,
        )
        self.addCleanup(self.stop_runner, process)
        self.assertIn(b'Waiting for exclusive access', self.read_line(process))
        return process

    def stop_runner(self, process):
        if process.poll() is None:
            process.kill()
        process.communicate(timeout=5)

    def read_line(self, process):
        self.assertTrue(select.select([process.stdout], [], [], 5)[0],
                        'Runner did not produce output')
        # Read one byte at a time so buffered data does not hide from select.
        line = b''
        while not line.endswith(b'\n'):
            byte = os.read(process.stdout.fileno(), 1)
            self.assertTrue(byte, 'Runner exited before producing a full line')
            line += byte
        return line

    def test_same_vm_waits_until_runner_exits(self):
        first = self.start_runner('shared')
        self.assertEqual(self.read_line(first), b'acquired\n')
        second = self.start_runner('shared')
        self.assertFalse(select.select([second.stdout], [], [], 0.1)[0])
        first.communicate(b'release\n', timeout=5)
        self.assertEqual(first.returncode, 0)
        self.assertEqual(self.read_line(second), b'acquired\n')
        second.communicate(b'release\n', timeout=5)
        self.assertEqual(second.returncode, 0)

    def test_different_vms_do_not_share_lock(self):
        first = self.start_runner('first')
        self.assertEqual(self.read_line(first), b'acquired\n')
        second = self.start_runner('second')
        self.assertEqual(self.read_line(second), b'acquired\n')


if __name__ == '__main__':
    unittest.main()
