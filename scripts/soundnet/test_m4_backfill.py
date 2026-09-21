#!/usr/bin/env python3
"""Tests for the M4 backfill selection rules. No database, no network."""
import os
import sys
import unittest

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
from m4_backfill import Row, load_types, select, DEFAULT_TYPES  # noqa: E402

TYPES = load_types(DEFAULT_TYPES)


def row(det_id, at, hex_="7c6d9f", code="B738", slant=4.0, candidates=1, peak=-6.0, heard="aircraft"):
    adsb = {"hex": hex_, "type_code": code, "slant_range_km": slant, "candidates_in_box": candidates}
    return Row(det_id, f"2026/09/x_{det_id}.wav", at, 0.5, heard, adsb, peak)


class SelectTest(unittest.TestCase):
    def test_keeps_one_clean_example(self):
        sel = select([row(1, 1000)], TYPES)
        self.assertEqual([r.detection_id for r, _ in sel.kept], [1])
        self.assertEqual(sel.kept[0][1]["coarse"], "jet")

    def test_rejects_an_ambiguous_sky(self):
        sel = select([row(1, 1000, candidates=2)], TYPES)
        self.assertEqual(sel.kept, [])
        self.assertEqual(sel.rejected["more than one aircraft in range"], 1)

    def test_rejects_clipped_audio_as_wind(self):
        sel = select([row(1, 1000, peak=0.0), row(2, 5000, peak=-0.1)], TYPES)
        self.assertEqual(sel.kept, [], "-0.1 dBFS is on the line and counts as clipped")
        self.assertEqual(sel.rejected["clipped (wind on the microphone)"], 2)

    def test_rejects_rows_with_no_level(self):
        sel = select([row(1, 1000, peak=None)], TYPES)
        self.assertEqual(sel.kept, [])

    def test_reports_unmapped_types_instead_of_guessing(self):
        sel = select([row(1, 1000, code="ZZZZ"), row(2, 2000, code="")], TYPES)
        self.assertEqual(sel.kept, [])
        self.assertEqual(sel.unmapped["ZZZZ"], 1)
        self.assertEqual(sel.rejected["no type code"], 1)

    def test_keeps_the_closest_window_of_each_pass(self):
        # One pass: three windows within a minute. The closest wins.
        rows = [row(1, 1000, slant=4.8), row(2, 1030, slant=3.1), row(3, 1060, slant=4.2)]
        sel = select(rows, TYPES)
        self.assertEqual([r.detection_id for r, _ in sel.kept], [2])
        self.assertEqual(sel.rejected["another window of the same pass"], 2)

    def test_a_long_gap_starts_a_new_pass(self):
        rows = [row(1, 1000, slant=4.0), row(2, 1000 + 600, slant=5.0)]
        sel = select(rows, TYPES)
        self.assertEqual(sorted(r.detection_id for r, _ in sel.kept), [1, 2])

    def test_different_aircraft_are_different_passes(self):
        rows = [row(1, 1000, hex_="aaaaaa"), row(2, 1010, hex_="bbbbbb", code="C208")]
        sel = select(rows, TYPES)
        self.assertEqual(sorted(r.detection_id for r, _ in sel.kept), [1, 2])
        classes = sorted(t["coarse"] for _, t in sel.kept)
        self.assertEqual(classes, ["jet", "prop"], "a Caravan is a prop to the microphone")

    def test_per_pass_can_keep_more(self):
        rows = [row(1, 1000, slant=4.8), row(2, 1030, slant=3.1), row(3, 1060, slant=4.2)]
        sel = select(rows, TYPES, per_pass=2)
        self.assertEqual(sorted(r.detection_id for r, _ in sel.kept), [2, 3])


if __name__ == "__main__":
    unittest.main()
