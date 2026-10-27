import unittest

from opennvr_common.throttle import Throttle


class ThrottleTest(unittest.TestCase):
    def test_dedup_same_key_within_interval(self):
        t = Throttle({}, min_interval=600, max_per_hour=100)
        self.assertTrue(t.allow("a", 1000))
        self.assertFalse(t.allow("a", 1500))
        self.assertTrue(t.allow("a", 1600))

    def test_different_keys_independent(self):
        t = Throttle({}, min_interval=600, max_per_hour=100)
        self.assertTrue(t.allow("a", 1000))
        self.assertTrue(t.allow("b", 1001))

    def test_custom_interval(self):
        t = Throttle({}, min_interval=600, max_per_hour=100)
        self.assertTrue(t.allow("a", 0, interval=86400))
        self.assertFalse(t.allow("a", 3600, interval=86400))
        self.assertTrue(t.allow("a", 86400, interval=86400))

    def test_global_rate_limit_and_suppressed_count(self):
        t = Throttle({}, min_interval=0, max_per_hour=3)
        self.assertEqual([t.allow(f"k{i}", 100 + i) for i in range(5)],
                         [True, True, True, False, False])
        self.assertEqual(t.take_suppressed(), 2)
        self.assertEqual(t.take_suppressed(), 0)
        self.assertTrue(t.allow("k9", 100 + 3600))

    def test_forget_undoes_allow(self):
        t = Throttle({}, min_interval=600, max_per_hour=1)
        self.assertTrue(t.allow("a", 10))
        t.forget("a", 10)
        self.assertTrue(t.allow("a", 11))

    def test_state_is_plain_dict_and_prunable(self):
        state = {}
        t = Throttle(state, min_interval=10, max_per_hour=10)
        t.allow("old", 0)
        t.allow("new", 10 * 86400)
        t.prune(10 * 86400, max_age=86400)
        self.assertEqual(set(state["last"]), {"new"})
        # Un estado reconstruido conserva la deduplicación.
        t2 = Throttle(state, min_interval=10, max_per_hour=10)
        self.assertFalse(t2.allow("new", 10 * 86400 + 1))


if __name__ == "__main__":
    unittest.main()
