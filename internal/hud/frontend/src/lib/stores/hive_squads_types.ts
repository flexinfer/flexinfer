// Shared squad-domain types used by the hive_squads store and panels.
// Field names are PascalCase to mirror the Go operator response (Go json
// tags here use the struct field names because the operator did not add
// json tags on store.Squad — that's the contract the HUD has to honor).

export interface Squad {
  ID: string;
  Name: string;
  Paths?: string[];
  Tests?: string[];
  Gates?: Record<string, unknown>;
  Ensemble?: Record<string, unknown>;
  BudgetShare?: number;
  RecursionEnabled?: boolean;
  Enabled?: boolean;
  LastLoadedSHA?: string;
  CreatedAt?: string;
  UpdatedAt?: string;
}

export interface SquadMemory {
  ID: number;
  SquadName: string;
  Kind: string;
  Title: string;
  Body?: string;
  Refs?: string[];
  Importance: number;
  CreatedAt?: string;
  LastSeenAt?: string;
}
