import os
import tempfile
import unittest

from egresswatch.tail import FileTail

HERE = os.path.dirname(os.path.abspath(__file__))


class FileTailTest(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory(dir=HERE)
        self.path = os.path.join(self.tmp.name, "log")

    def tearDown(self):
        self.tmp.cleanup()

    def append(self, text):
        with open(self.path, "a") as fh:
            fh.write(text)

    def test_missing_file(self):
        self.assertEqual(FileTail(self.path, {}).read_lines(), [])

    def test_incremental_and_partial_lines(self):
        state = {}
        t = FileTail(self.path, state)
        self.append("a\nb\npar")
        self.assertEqual(t.read_lines(), ["a", "b"])
        self.append("tial\n")
        self.assertEqual(t.read_lines(), ["partial"])
        self.assertEqual(t.read_lines(), [])
        # Un lector nuevo con el estado guardado continúa donde estaba.
        self.append("c\n")
        self.assertEqual(FileTail(self.path, state).read_lines(), ["c"])

    def test_truncation_and_rotation(self):
        t = FileTail(self.path, {})
        self.append("one\ntwo\n")
        t.read_lines()
        with open(self.path, "w") as fh:
            fh.write("x\n")
        self.assertEqual(t.read_lines(), ["x"])
        os.rename(self.path, self.path + ".1")
        self.append("new\n")
        self.assertEqual(t.read_lines(), ["new"])

    def test_truncate_if_larger_only_after_full_read(self):
        t = FileTail(self.path, {})
        self.append("x" * 50 + "\n")
        self.assertFalse(t.truncate_if_larger(10), "aún no leído")
        t.read_lines()
        self.assertFalse(t.truncate_if_larger(1000))
        self.assertTrue(t.truncate_if_larger(10))
        self.assertEqual(os.path.getsize(self.path), 0)
        self.append("y\n")
        self.assertEqual(t.read_lines(), ["y"])


if __name__ == "__main__":
    unittest.main()
