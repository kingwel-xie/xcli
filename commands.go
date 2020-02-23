package xcli

import (
	"bufio"
	"context"
	"fmt"
	"github.com/gogo/protobuf/proto"
	"github.com/ipfs/go-cid"
	"github.com/ipfs/go-datastore/query"
	dsync "github.com/ipfs/go-datastore/sync"
	logging "github.com/ipfs/go-log"
	"github.com/libp2p/go-libp2p-core/host"
	"github.com/libp2p/go-libp2p-core/network"
	"github.com/libp2p/go-libp2p-core/peerstore"
	"github.com/libp2p/go-libp2p-core/routing"
	dht "github.com/libp2p/go-libp2p-kad-dht"
	"github.com/libp2p/go-libp2p/p2p/protocol/ping"
	"github.com/opentracing/opentracing-go/log"
	"github.com/pkg/errors"
	"github.com/urfave/cli/v2"
	"net"
	"sort"
	"strings"
	"sync"
	"time"

	ds "github.com/ipfs/go-datastore"
	"github.com/libp2p/go-libp2p-core/peer"
	recpb "github.com/libp2p/go-libp2p-record/pb"
	ma "github.com/multiformats/go-multiaddr"
)


type P2PNode struct {
	host.Host
	Dht *dht.IpfsDHT
	Ds  *dsync.MutexDatastore
}

var (
	IPFS_PEERS = convertPeers([]string{
		//"/ip4/128.199.219.111/tcp/4001/p2p/QmSoLSafTMBsPKadTEgaXctDQVcqN88CNLHXMkTNwMKPnu",
		//"/ip4/104.236.76.40/tcp/4001/p2p/QmSoLV4Bbm51jM9C4gDYZQ9Cy3U6aXMJDAbzgu2fzaDs64",
		//"/ip4/104.131.131.82/tcp/4001/p2p/QmaCpDMGvV2BGHeYERUEnRQAwe3N8SzbUtfsmvsqQLuvuJ",
		//"/ip4/104.236.179.241/tcp/4001/p2p/QmSoLPppuBtQSGwKDZT2M73ULpjvfd3aZ6ha4oFGL1KrGM",
		//"/ip4/178.62.158.247/tcp/4001/p2p/QmSoLer265NRgSp2LA3dPaeykiS1J6DifTC88f5uVQKNAd",
		"/ip4/47.100.12.133/tcp/4001/p2p/QmQ4Z47LeGLJ8SanLPaBF3rWWfqKqmanAkCs9CgeYF2TAp",
		//"/ip4/47.100.12.133/tcp/4443/p2p/QmXtW5fXrrvmHWPhq3FLHdm4zKnC5FZdhTRynSQT57Yrmd",
	})
)

func convertPeers(peers []string) []peer.AddrInfo {
	pinfos := make([]peer.AddrInfo, len(peers))
	for i, addr := range peers {
		maddr := ma.StringCast(addr)
		p, err := peer.AddrInfoFromP2pAddr(maddr)
		if err != nil {
			log.Error(err)
			return nil
		}
		pinfos[i] = *p
	}
	return pinfos
}

func RunP2PNodeCLI(node *P2PNode, args... cli.Commands) {
	for _,c := range args {
		cliCmds = append(cliCmds, c...)
	}

	RunCli(cliCmds, func(m map[string]interface{}) {
		m["P2PNode"] = node
	})
}

var cliCmds = cli.Commands{
	{
		Name:     "id",
		Category: "p2p",
		Usage:       "id",
		Description: "show my id and addresses",
		Action: func(c *cli.Context) error {
			node := c.App.Metadata["P2PNode"].(*P2PNode)
			fmt.Fprintln(c.App.Writer, node.ID(), node.Addrs())
			return nil
		},
	},
	{
		Name:        "dump-ds",
		Aliases:     []string{"dd"},
		Category:    "p2p",
		Usage:       "dump-ds",
		Description: "dump dht data store in details",
		Action: func(c *cli.Context) error {
			node := c.App.Metadata["P2PNode"].(*P2PNode)
			DumpDhtDS(node)
			return nil
		},
	},
	{
		Name:        "query",
		Aliases:     []string{"qu"},
		Category:    "dht",
		Usage:       "query <key>",
		Description:   "start refreshing dht to get some closest peers",
		Action: func(c *cli.Context) error {
			node := c.App.Metadata["P2PNode"].(*P2PNode)
			QueryPeer(c, node, c.Args().First())
			return nil
		},
	},
	{
		Name:        "put-value",
		Aliases:     []string{"pv"},
		Category:    "dht",
		Usage:       "put <key> <value>",
		Description:   "get k/v to dht routing table",
		Action: func(c *cli.Context) error {
			if c.Args().Len() != 2 {
				fmt.Fprintln(c.App.Writer, "Bad argument")
				return nil
			}
			node := c.App.Metadata["P2PNode"].(*P2PNode)
			PutKeyValue(node, c.Args().First(), c.Args().Get(1))
			return nil
		},
	}, {
		Name:        "get-value",
		Aliases:     []string{"gv"},
		Category:    "dht",
		Usage:       "get <key>",
		Description:   "get k/v from dht routing table",
		Action: func(c *cli.Context) error {
			if c.Args().Len() != 1 {
				fmt.Fprintln(c.App.Writer, "Bad argument")
				return nil
			}
			node := c.App.Metadata["P2PNode"].(*P2PNode)
			GetKeyValue(c, node, c.Args().First())
			return nil
		},
	},
	{
		Name:        "findprovs",
		Aliases:     []string{"fv"},
		Category:    "dht",
		Usage:       "findprovs <cid>",
		Description:   "find provider peer-id with specified CID",
		Action: func(c *cli.Context) error {
			if c.Args().Len() != 1 {
				fmt.Fprintln(c.App.Writer, "Bad argument")
				return nil
			}
			node := c.App.Metadata["P2PNode"].(*P2PNode)
			FindProviders(node, c.Args().First())
			return nil
		},
	},
	{
		Name:        "provide",
		Aliases:     []string{"pr"},
		Category:    "dht",
		Usage:       "provide <cid>",
		Description:   "provide CID to dht routing table",
		Action: func(c *cli.Context) error {
			if c.Args().Len() != 1 {
				fmt.Fprintln(c.App.Writer, "Bad argument")
				return nil
			}
			node := c.App.Metadata["P2PNode"].(*P2PNode)
			ProvideCid(node, c.Args().First())
			return nil
		},
	},
	{
		Name:        "find-peer",
		Aliases:     []string{"fp"},
		Category:    "dht",
		Usage:       "find-peer <cid>",
		Action: func(c *cli.Context) error {
			if c.Args().Len() != 1 {
				fmt.Fprintln(c.App.Writer, "Bad argument")
				return nil
			}
			node := c.App.Metadata["P2PNode"].(*P2PNode)
			FindPeer(c, node, c.Args().First())
			return nil
		},
	},
	{
		Name:        "dump-bucket",
		Aliases:     []string{"db"},
		Category:    "dht",
		Usage:       "dump-bucket",
		Action: func(c *cli.Context) error {
			if c.Args().Present() {
				fmt.Fprintln(c.App.Writer, "Bad argument")
				return nil
			}
			node := c.App.Metadata["P2PNode"].(*P2PNode)
			node.Dht.RoutingTable().Print()
			return nil
		},
	},
	{
		Name:        "refresh",
		Aliases:     []string{"re"},
		Category:    "dht",
		Usage:       "refresh",
		Description:   "refresh - refresh dht routing table",
		Action: func(c *cli.Context) error {
			node := c.App.Metadata["P2PNode"].(*P2PNode)
			node.Dht.RefreshRoutingTable()
			return nil
		},
	},
	{
		Name:        "bootstrap",
		Aliases:     []string{"boot"},
		Category:    "dht",
		Usage:       "bootstrap",
		Description:   "bootstrap - refresh dht routing table",
		Action: func(c *cli.Context) error {
			node := c.App.Metadata["P2PNode"].(*P2PNode)
			bootstrapConnect(c.Context, node, IPFS_PEERS)
			return nil
		},
	},
	{
		Name:        "connect",
		Aliases:     []string{"con"},
		Category:    "p2p",
		Usage:       "connect <multiaddr>",
		Action: func(c *cli.Context) error {
			if c.Args().Len() != 1 {
				fmt.Fprintln(c.App.Writer, "Bad argument")
				return nil
			}
			node := c.App.Metadata["P2PNode"].(*P2PNode)
			e := connectPeer(c.Context, node, c.Args().First())
			fmt.Fprintln(c.App.Writer, e)
			return nil
		},
	},	{
		Name:        "disconnect",
		Aliases:     []string{"disc"},
		Category:    "p2p",
		Usage:       "disconnect <peer-id>",
		Action: func(c *cli.Context) error {
			if c.Args().Len() != 1 {
				fmt.Fprintln(c.App.Writer, "Bad argument")
				return nil
			}
			node := c.App.Metadata["P2PNode"].(*P2PNode)
			e := disconnectPeer(c.Context, node, c.Args().First())
			fmt.Fprintln(c.App.Writer, e)
			return nil
		},
	},
	{
		Name:        "test-stream",
		Aliases:     []string{"tt"},
		Usage:       "test-stream <peer-id>",
		Action: func(c *cli.Context) error {
			if c.Args().Len() != 1 {
				fmt.Fprintln(c.App.Writer, "Bad argument")
				return nil
			}
			node := c.App.Metadata["P2PNode"].(*P2PNode)
			e := testStream(c.Context, node, c.Args().First())
			fmt.Fprintln(c.App.Writer, e)
			return nil
		},
	},
	{
		Name:        "test-conn",
		Aliases:     []string{"tc"},
		Usage:       "test-conn <peer-id>",
		Action: func(c *cli.Context) error {
			if c.Args().Len() != 1 {
				fmt.Fprintln(c.App.Writer, "Bad argument")
				return nil
			}
			e := testConn(c.Context, c.Args().First())
			fmt.Fprintln(c.App.Writer, e)
			return nil
		},
	},
	{
		Name:        "show",
		Aliases: []string{"s"},
		Category:    "show",
		Usage:       "show [sub-level]",
		Description:   "show - show connections, streams, logs and such",
		HideHelp:    true,
		Subcommands: []*cli.Command{
			&cli.Command{
				Name:    "protocols",
				Aliases: []string{"proto"},
				Usage:       "show supported protocols",
				Action: func(c *cli.Context) error {
					node := c.App.Metadata["P2PNode"].(*P2PNode)
					fmt.Fprintln(c.App.Writer, node.Mux().Protocols())
					return nil
				},
			},
			&cli.Command{
				Name:    "addresses",
				Aliases: []string{"addr"},
				Usage:       "show known addresses",
				Action: func(c *cli.Context) error {
					showAddresses(c.App.Metadata["P2PNode"].(*P2PNode))
					return nil
				},
			},
			&cli.Command{
				Name:    "peer-store",
				Aliases: []string{"ps"},
				Usage:       "show peer data store",
				Action: func(c *cli.Context) error {
					showPeerStore(c.App.Metadata["P2PNode"].(*P2PNode))
					return nil
				},
			},
			&cli.Command{
				Name:    "connections",
				Aliases: []string{"con"},
				Usage:       "show connections",
				Action: func(c *cli.Context) error {
					showConnections(c.App.Metadata["P2PNode"].(*P2PNode))
					return nil
				},
			},
			&cli.Command{
				Name:    "streams",
				Aliases: []string{"str"},
				Usage:       "show streams",
				Action: func(c *cli.Context) error {
					showStreams(c.App.Metadata["P2PNode"].(*P2PNode), "")
					return nil
				},
			},
			&cli.Command{
				Name:        "logs",
				Usage:       "show logs",
				Description: "show all log entities", // & levels",
				Action: func(c *cli.Context) error {
					l := logging.GetSubsystems()
					fmt.Fprintln(c.App.Writer, l)
					return nil
				},
			},
		},
	},
	{
		Name:        "ping",
		Category:    "p2p",
		Usage:       "ping <peerid>",
		Description:   "ping - ping peer id, will expire in 5s",
		Action: func(c *cli.Context) error {
			if c.Args().Len() != 1 {
				fmt.Fprintln(c.App.Writer, "Bad argument")
				return nil
			}
			node := c.App.Metadata["P2PNode"].(*P2PNode)
			PingHost(c.Context, node, c.Args().First())
			return nil
		},
		Before: func(c *cli.Context) error {
			//fmt.Fprintf(c.App.Writer, "brace for impact\n")
			return nil
		},
		After: func(c *cli.Context) error {
			//fmt.Fprintf(c.App.Writer, "did we lose anyone?\n")
			return nil
		},
	},
	{
		Name:    "log",
		Aliases: []string{"l"},
		//Category:    "loglevel",
		Usage:       "log <name> level",
		Description: "log level management",
		HideHelp:        true,
		/*
			Action: func(c *cli.Context) error {
				fmt.Fprintln(c.App.Writer, "top log")
				return nil
			},

		*/
		Subcommands: []*cli.Command{
			&cli.Command{
				//Category:    "log",
				Name:        "level",
				Usage:       "log level <name|all> <debug|info|warning|error|critical>",
				Description: "change log level",
				Action: func(c *cli.Context) error {
					subsystem := c.Args().First()
					level := c.Args().Get(1)
					if subsystem == "all" {
						subsystem = "*"
					}
					if err := logging.SetLogLevel(subsystem, level); err != nil {
						fmt.Fprintln(c.App.Writer, err)
						return nil
					}

					fmt.Fprintf(c.App.Writer, "Changed log level of '%s' to '%s'\n", subsystem, level)
					return nil
				},
			},
		},
	},
}



// This code is borrowed from the go-ipfs bootstrap process
func bootstrapConnect(ctx context.Context, ph host.Host, peers []peer.AddrInfo) error {
	if len(peers) < 1 {
		return errors.New("not enough bootstrap peers")
	}

	errs := make(chan error, len(peers))
	var wg sync.WaitGroup
	for _, p := range peers {

		// performed asynchronously because when performed synchronously, if
		// one `Connect` call hangs, subsequent calls are more likely to
		// fail/abort due to an expiring context.
		// Also, performed asynchronously for dial speed.

		wg.Add(1)
		go func(p peer.AddrInfo) {
			defer wg.Done()
			defer fmt.Println(ctx, "bootstrapDial", ph.ID(), p.ID)
			fmt.Printf("%s bootstrapping to %s\n", ph.ID(), p.ID)

			ph.Peerstore().AddAddrs(p.ID, p.Addrs, peerstore.PermanentAddrTTL)
			if err := ph.Connect(ctx, p); err != nil {
				fmt.Println(ctx, "bootstrapDialFailed", p.ID, err)
				errs <- err
				return
			}
			fmt.Println("bootstrapped with", p.ID)
		}(p)
	}
	wg.Wait()

	// our failure condition is when no connection attempt succeeded.
	// So drain the errs channel, counting the results.
	close(errs)
	count := 0
	var err error
	for err = range errs {
		if err != nil {
			count++
		}
	}
	if count == len(peers) {
		return fmt.Errorf("failed to bootstrap. %s", err)
	}
	return nil
}
////////////////////////////////////////////////////////////////////////////////////////////////////////////////////////
func showPeerStore(node *P2PNode) {
	var sum int
	for _, pp := range node.Peerstore().Peers() {
		fmt.Println(node.Peerstore().PeerInfo(pp))
		sum++
	}
	fmt.Println(time.Now(), sum, "Peers")
}

func showConnections(node *P2PNode) {
	var sum int
	for _, nn := range node.Network().Conns() {
		fmt.Println(nn.RemotePeer(), nn.RemoteMultiaddr(), node.Peerstore().LatencyEWMA(nn.RemotePeer()), directionString(nn.Stat().Direction))
		sum ++
	}
	fmt.Println(time.Now(), sum, "Connections")
}

func showAddresses(node *P2PNode) {
	addrs := make(map[peer.ID][]ma.Multiaddr)
	ps := node.Network().Peerstore()
	for _, p := range ps.Peers() {
		addrs[p] = append(addrs[p], ps.Addrs(p)...)
		sort.Slice(addrs[p], func(i, j int) bool {
			return addrs[p][i].String() < addrs[p][j].String()
		})
	}

	fmt.Println(addrs)
}

func directionString(direction network.Direction) string {
	switch direction {
	case network.DirInbound:
		return "Inbound"
	case network.DirOutbound:
		return "Outbound"
	default:
		return "Unknown"
	}
}

func showStreams(node *P2PNode, peerid peer.ID) {
	for _, nn := range node.Network().Conns() {
		fmt.Println(nn.RemotePeer(), nn.RemoteMultiaddr(), node.Peerstore().LatencyEWMA(nn.RemotePeer()), directionString(nn.Stat().Direction))
		for index, ss := range nn.GetStreams() {
			fmt.Println(index, ss.Protocol(), directionString(ss.Stat().Direction))
		}
	}
}

func QueryPeer(c *cli.Context, n *P2PNode, key string) {
	if n.Dht == nil {
		fmt.Println("Not DHT")
		return
	}

	ctx, cancel := context.WithCancel(c.Context)
	ctx, events := routing.RegisterForQueryEvents(ctx)
	defer cancel()

	_, err := n.Dht.GetClosestPeers(ctx, key)
	if err != nil {
		fmt.Println("DHT", err)
		return
	}

	for e := range events {
		fmt.Println(e)
	}

	//for p := range closestPeers {
	//	fmt.Println(p.Pretty())
	//}
	//fmt.Println(n)
}

func FindPeer(c *cli.Context, n *P2PNode, peerid string) {
	if n.Dht == nil {
		fmt.Println("Not DHT")
		return
	}

	pid, err := peer.Decode(peerid)
	if err != nil {
		fmt.Println("peer ID", err)
		return
	}

	ctx, cancel := context.WithCancel(c.Context)
	defer cancel()

	pi, err := n.Dht.FindPeer(ctx, pid)
	if err != nil {
		fmt.Println("Find peer", err)
		return
	}

	fmt.Println("Peer found", pi)
}

func GetKeyValue(c *cli.Context, n *P2PNode, key string) {
	if n.Dht == nil {
		fmt.Println("Not DHT")
		return
	}

	ctx, cancel := context.WithCancel(c.Context)
	defer cancel()

	val, err := n.Dht.GetValue(ctx, key)
	if err != nil {
		fmt.Println("Get failed", err)
		return
	}

	fmt.Fprintln(c.App.Writer, "Value", string(val))
}

func PutKeyValue(n *P2PNode, key, value string) {
	if n.Dht == nil {
		fmt.Println("Not DHT")
		return
	}
	if len(n.Network().Conns()) == 0 {
		fmt.Println("cannot put value, no connected peers")
		//return
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	err := n.Dht.PutValue(ctx, key, []byte(value))

	if err != nil {
		fmt.Println("Put failed", err)
	}
}

func DumpDhtDS(n *P2PNode) {
	if n.Dht == nil {
		fmt.Println("Not DHT")
		return
	}

	r, err := n.Ds.Query(query.Query{Prefix: ""})
	if err != nil {
		return
	}

	all, err := r.Rest()
	if err != nil {
		return
	}

	for _, e := range all {
		buf, _ := n.Ds.Get(ds.NewKey(e.Key))

		// decode provider data
		if strings.HasPrefix(e.Key,"/provider") {
			ttt := strings.Split(e.Key, "/")
			fmt.Println(ttt)
		} else {
			rec := new(recpb.Record)
			err = proto.Unmarshal(buf, rec)
			fmt.Println(e.Key, string(rec.Key), string(rec.Value))
		}
	}
}

func FindProviders(n *P2PNode, key string) {
	if n.Dht == nil {
		fmt.Println("Not DHT")
		return
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	//ctx, events := routing.RegisterForQueryEvents(ctx)

	numProviders := 3 // by default
	if numProviders < 1 {
		fmt.Println("number of providers must be greater than 0")
		return
	}

	cid, err := cid.Parse(key)
	if err != nil {
		fmt.Println(key, err)
		return
	}

	pChan := n.Dht.FindProvidersAsync(ctx, cid, numProviders)
	for p := range pChan {
		//peer.AddrInfo
		fmt.Println(p)
	}
}

func ProvideCid(n *P2PNode, key string) {
	if n.Dht == nil {
		fmt.Println("Not DHT")
		return
	}
	if len(n.Network().Conns()) == 0 {
		fmt.Println("cannot provide, no connected peers")
		return
	}

	cid, err := cid.Decode(key)
	if err != nil {
		fmt.Println(key, err)
		return
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	//ctx, events := routing.RegisterForQueryEvents(ctx)

	provideErr := provideKeys(ctx, n.Dht, cid)
	if provideErr != nil {
	}
}

func provideKeys(ctx context.Context, r routing.Routing, args ...cid.Cid) error {
	var cids []cid.Cid
	cids = append(cids, args...)

	for _, c := range cids {
		err := r.Provide(ctx, c, true)
		if err != nil {
			fmt.Println("Failed to provide", c, err)
			return err
		}
	}
	return nil
}

func connectPeer(ctx context.Context, node *P2PNode, addr string) error {
	// The following code extracts target's the peer ID from the
	// given multiaddress
	ipfsaddr, err := ma.NewMultiaddr(addr)
	if err != nil {
		return err
	}

	addrinfo, err := peer.AddrInfoFromP2pAddr(ipfsaddr)
	if err != nil {
		return err
	}
	return node.Connect(ctx, *addrinfo)
}

func disconnectPeer(ctx context.Context, node *P2PNode, peerid string) error {
	pid, err := peer.Decode(peerid)
	if err != nil {
		return err
	}

	return node.Network().ClosePeer(pid)
}

func PingHost(ctx context.Context, node *P2PNode, peerid string) {
	pid, err := peer.Decode(peerid)
	if err != nil {
		fmt.Println("peer ID", err)
		return
	}

	ctx2, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	out := ping.Ping(ctx2, node, pid)

	var sum int
	for r := range out {
		fmt.Println(r)
		sum ++
		if sum >= 5 {
			break
		}
	}
}

func testStream (ctx context.Context, node *P2PNode, peerid string) error {
	pid, err := peer.Decode(peerid)
	if err != nil {
		return err
	}

	// make a new stream
	stream, err := node.NewStream(ctx, pid, TestProtocol)
	if err != nil {
		return err
	}
	defer stream.Reset()

	ch := make(chan struct{}, 1)
	go func() {
		defer func() {
			ch <- struct{}{}
		}()

		b := make([]byte, 4096)
		ss := bufio.NewReader(stream)
		var sum int
		for {
			n, err := ss.Read(b)
			if err != nil {
				stream.Reset()
				return
			}

			sum += n
			if sum > 100*1024*1024 {
				sum = 0
				fmt.Println("100M")
			}
		}
	}()

	select {
	case <- ctx.Done():
		err = ctx.Err()
	case <- ch:
		err = nil
	}

	return err
}


const TestProtocol = "/test/0.0.1"
func ServeTest(ctx context.Context, node *P2PNode, limit int64) {
	// Set a stream handler on host A. /echo/1.0.0 is
	// a user-defined protocol name.
	node.SetStreamHandler(TestProtocol, func(s network.Stream) {
		fmt.Println("Got a new stream!", s.Conn().RemotePeer())

		ctx2, cancel := context.WithCancel(ctx)

		go func() {
			defer func() {
				cancel()
				s.Reset()
			}()

			var sum int64
			w := bufio.NewWriterSize(s, 65536)
			b := make([]byte, 4096)
			t := time.Now()
			for {
				n, err := w.Write(b)
				if err != nil {
					fmt.Println("Stream write  error", err)
					break
				}

				sum += int64(n)
				if sum >= limit * 1024 * 1024 {
					//log.Warning("Hit limit", sum)
					break
				}
			}

			elapsed := time.Since(t)
			rate := float64(sum/1024/1024)/elapsed.Seconds()
			fmt.Printf("Copied=%dM, rate=%.2fMB/s, elapsed=%s\n", sum/1024/1024, rate, elapsed)
		}()

		<-ctx2.Done()
		s.Reset()
	})
}


func ServeTest2(ctx context.Context, limit int64) {
	l, err := net.Listen("tcp4", ":8848")
	if err != nil {
		fmt.Println(err)
		return
	}

	go func() {
		<- ctx.Done()
		l.Close()
	}()

	for {
		c, err := l.Accept()
		if err != nil {
			log.Error(err)
			break
		}

		go func() {
			ctx2, cancel := context.WithCancel(ctx)
			go func() {
				defer cancel()

				var sum int64
				w := bufio.NewWriterSize(c, 65536)
				b := make([]byte, 4096)
				t := time.Now()
				for {
					n, err := w.Write(b)
					if err != nil {
						fmt.Println("Stream write  error", err)
						break
					}

					sum += int64(n)
					if sum >= limit * 1024 * 1024 {
						//log.Warning("Hit limit", sum)
						break
					}
				}

				elapsed := time.Since(t)
				rate := float64(sum/1024/1024)/elapsed.Seconds()
				fmt.Printf("Copied=%dM, rate=%.2fMB/s, elapsed=%s\n", sum/1024/1024, rate, elapsed)
			}()

			<-ctx2.Done()
			c.Close()
		}()
	}
}


func testConn (ctx context.Context, addr string) error {

	c, err := net.Dial("tcp4", addr)
	if err != nil {
		return err
	}
	defer c.Close()

	ch := make(chan struct{}, 1)
	go func() {
		defer func() {
			ch <- struct{}{}
		}()

		b := make([]byte, 4096)
		ss := bufio.NewReaderSize(c, 65536)
		var sum int
		for {
			n, err := ss.Read(b)
			if err != nil {
				c.Close()
				return
			}

			sum += n
			if sum > 100*1024*1024 {
				sum = 0
				fmt.Println("100M")
			}
		}
	}()

	select {
	case <- ctx.Done():
		err = ctx.Err()
	case <- ch:
		err = nil
	}

	return err
}
