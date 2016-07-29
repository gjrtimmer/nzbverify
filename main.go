package main

import (
    "flag"
    "fmt"
    "os"
    "path/filepath"
    "runtime"
    "sync"
    "time"

    "github.com/GJRTimmer/nzb"
    "github.com/GJRTimmer/nntp"
    "github.com/cheggaaa/pb"
)

var (
    // Application
    maxCPU int

    // Server
    host string
    port uint
    tls bool
    username string
    password string
    maxConn int

    nzbFile string
)

func init() {
    flag.IntVar(&maxCPU, "cpu", 4, "Max CPU's, defaults to 4")
    flag.StringVar(&nzbFile, "nzb", "", "NZB File")
    flag.StringVar(&host, "host", "", "NNTP Host")
    flag.UintVar(&port, "port", 119, "NNTP Port")
    flag.BoolVar(&tls, "tls", false, "Use TLS")
    flag.StringVar(&username, "user", "", "NNTP Username")
    flag.StringVar(&password, "pass", "", "NNTP Password")
    flag.IntVar(&maxConn, "max", 8, "Max connections")

    flag.Parse()

    if len(nzbFile) < 1 {
        fmt.Println("No NZB file provided.")
        os.Exit(1)
    }

    if len(host) < 1 {
        fmt.Println("No NNTP host provided.")
        os.Exit(2)
    }
}

func main() {

	// Program can run with multiple CPU's
    runtime.GOMAXPROCS(maxCPU)

    nFile, err := os.Open(nzbFile)
    defer nFile.Close()
    if err != nil {
        panic(err)
    }

    fmt.Printf("Parsing NZB...")
    n, err := nzb.Parse(nFile)
    if err != nil {
        panic(err)
    }
    fmt.Println("[DONE]")

    chunks := n.GenerateChunkList()

    jobQueue := make(chan *nntp.Request, maxConn + 10)
    s := &nntp.ServerInfo {
        Host: host,
        Port: uint16(port),
        TLS: tls,
        Auth: &nntp.ServerAuth {
            Username: username,
            Password: password,
        },
    }

    connPool := nntp.NewConnectionPool(s, jobQueue, maxConn)
    connPool.Start()

    fmt.Printf("\nVerify: %s\n", filepath.Base(nzbFile))
    fmt.Printf("  |- %s%d\n", fmt.Sprintf("%-20s", "FileSet(s): "), len(n.FileSets))
    for _, fs := range n.FileSets {
        fmt.Printf("  |  - %s%s\n", fmt.Sprintf("%-18s", "FileSet: "), fs.Name)
        fmt.Printf("  |    - %s%d\n", fmt.Sprintf("%-16s", "Files: "), len(fs.Files))
        fmt.Printf("  |    - %s%s\n", fmt.Sprintf("%-16s", "Size: "), fs.Size)
        fmt.Printf("  |    - %s\n", fmt.Sprintf("%-16s", "ParSet: "))
        fmt.Printf("  |      - %s%d\n", fmt.Sprintf("%-14s", "Files: "), len(fs.ParSet.Files) + 1) // +1 because parent .par2 file is being held in own structure place
        fmt.Printf("  |      - %s%s\n", fmt.Sprintf("%-14s", "Size: "), fs.ParSet.Size)
        fmt.Printf("  |      - %s%d\n", fmt.Sprintf("%-14s", "Blocks: "), fs.ParSet.TotalBlocks)
    }
    fmt.Printf("  |- %s%s\n", fmt.Sprintf("%-20s", "Total Size: "), n.Size)
    fmt.Printf("  |- %s%d\n", fmt.Sprintf("%-20s", "Total Segments: "), chunks.Total)
    fmt.Printf("  |- %s%s (TLS: %t)\n", fmt.Sprintf("%-20s", "Host: "), host, tls)
    fmt.Printf("  |- %s%d\n\n", fmt.Sprintf("%-20s", "Connections: "), maxConn)

    bar := pb.StartNew(chunks.Total)

    var available int
    start := time.Now()
    completionTicker := time.NewTicker(1 * time.Second)
    wg := new(sync.WaitGroup)
    responses := connPool.Collect()

    wg.Add(1)
    go func(wg *sync.WaitGroup) {

        defer func() {
            connPool.Stop()
            wg.Done()
        }()

        for {
            select {
            case r := <- responses:
                go newWork(chunks, jobQueue)
                if r.Article.Exists {
                    available++
                }
                bar.Increment()

            case <- completionTicker.C:
                if chunks.Marker == chunks.Total {
                    bar.FinishPrint("")
                    return
                }
            }
        }
    }(wg)

    // Create initial work
    initWork := chunks.GetChunks(maxConn + 10)
    for _, w := range initWork {
        req := nntp.NewRequest(w.Segment.ID, w.Groups, nntp.CHECK)
        jobQueue <- req
    }

    wg.Wait()

    // Print Report
    fmt.Printf("Verification Report: %s", filepath.Base(nzbFile))
    fmt.Printf("  |- %s%d\n", fmt.Sprintf("%-20s", "FileSet(s): "), len(n.FileSets))
    for _, fs := range n.FileSets {
        fmt.Printf("  |  - %s%s\n", fmt.Sprintf("%-18s", "FileSet: "), fs.Name)
        fmt.Printf("  |    - %s%d\n", fmt.Sprintf("%-16s", "Files: "), len(fs.Files))
        fmt.Printf("  |    - %s%s\n", fmt.Sprintf("%-16s", "Size: "), fs.Size)
        fmt.Printf("  |    - %s\n", fmt.Sprintf("%-16s", "ParSet: "))
        fmt.Printf("  |      - %s%d\n", fmt.Sprintf("%-14s", "Files: "), len(fs.ParSet.Files) + 1) // +1 because parent .par2 file is being held in own structure place
        fmt.Printf("  |      - %s%s\n", fmt.Sprintf("%-14s", "Size: "), fs.ParSet.Size)
        fmt.Printf("  |      - %s%d\n", fmt.Sprintf("%-14s", "Blocks: "), fs.ParSet.TotalBlocks)
    }
    fmt.Printf("  |- %s%s\n", fmt.Sprintf("%-20s", "Total Size: "), n.Size)
    fmt.Printf("  |- %s%d\n", fmt.Sprintf("%-20s", "Total Segments: "), chunks.Total)
    fmt.Printf("  |- %s%d\n", fmt.Sprintf("%-20s", "Available Segments: "), available,  )
    fmt.Printf("  |- %s%6.2f %%\n", fmt.Sprintf("%-20s", "Completion: "), (float64(available) / float64(chunks.Total)) * 100)
    fmt.Printf("  |- %s%s (TLS: %t)\n", fmt.Sprintf("%-20s", "Host: "), host, tls)
    fmt.Printf("  |- %s%d\n", fmt.Sprintf("%-20s", "Connection(s): "), maxConn)
    fmt.Printf("  |- %s%s\n", fmt.Sprintf("%-20s", "Verification Time: "), time.Since(start))

    os.Exit(0)
}

func newWork(chunks *nzb.Chunks, queue chan *nntp.Request) {
    c := chunks.GetNext()
    if c != nil {
        req := nntp.NewRequest(c.Segment.ID, c.Groups, nntp.CHECK)
        queue <- req
    }
}

// EOF
