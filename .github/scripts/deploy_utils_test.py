import unittest
from deploy_utils import retry

class RetryTest(unittest.TestCase):
    def test_retries_until_action_succeeds(self):
        calls = []
        sleeps = []
        def action():
            calls.append(1)
            if len(calls) < 3:
                raise RuntimeError("transient")
            return "ok"
        result = retry(action, attempts=4, delay_seconds=lambda n: n, sleep=sleeps.append)
        self.assertEqual(result, "ok")
        self.assertEqual(len(calls), 3)
        self.assertEqual(sleeps, [1, 2])

    def test_raises_last_error_after_bound(self):
        with self.assertRaisesRegex(RuntimeError, "still down"):
            retry(lambda: (_ for _ in ()).throw(RuntimeError("still down")), attempts=2, delay_seconds=lambda _: 0, sleep=lambda _: None)

if __name__ == "__main__":
    unittest.main()
