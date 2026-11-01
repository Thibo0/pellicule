package main

// diskFree n'est pas mesuré sous Windows (développement uniquement).
func diskFree(path string) int64 { return -1 }
