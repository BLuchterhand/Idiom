import unittest

from labels import short_label


class ShortLabelTest(unittest.TestCase):
    def test_short_label(self):
        self.assertEqual(short_label(), "Save")


if __name__ == "__main__":
    unittest.main()
