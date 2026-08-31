import unittest
from check_identity_leaks import blocked_offsets

class IdentityLeakCheckTest(unittest.TestCase):
    def test_detects_blocked_identity_case_insensitively(self):
        term = "".join(map(chr, [114, 111, 99, 107]))
        self.assertEqual(blocked_offsets("prefix " + term.upper() + " suffix"), [7])

    def test_detects_unrelated_contributor_identity(self):
        term = "".join(map(chr, [108, 97, 107, 104, 97, 110, 109, 97, 108, 105, 49, 54, 57, 45, 99, 108, 111, 117, 100]))
        self.assertEqual(blocked_offsets("prefix " + term + " suffix"), [7])

    def test_allows_unrelated_content(self):
        self.assertEqual(blocked_offsets("sys0 private deployment"), [])

if __name__ == "__main__":
    unittest.main()
