import Go2Lean.Driver
import Go2Lean.Generated.Registry

def main (args : List String) : IO UInt32 :=
  Go2Lean.Search.driverMain Go2Lean.Generated.registry Go2Lean.Generated.jsonTables args
