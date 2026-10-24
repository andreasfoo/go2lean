"""Write a whole solution file for the judge from a challenge and an attack
(testdata/redteam/solutions/*.json): the attack's imports first, its
prelude before the first theorem, its proof for the first sorry.
usage: python3 solution.py <Challenge.lean> <attack.json> > Solution.lean"""
import json, sys

challenge = open(sys.argv[1]).read()
attack = json.load(open(sys.argv[2]))
i = challenge.index("\ntheorem ")
text = challenge[:i] + "\n" + attack["prelude"] + challenge[i:]
text = text.replace("  sorry", attack["proof"], 1)
print(attack["imports"] + "\n" + text, end="")
