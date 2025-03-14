// Package unixfs implements a data format for files in the IPFS filesystem It
// is not the only format in ipfs, but it is the one that the filesystem
// assumes
package unixfs

import (
	"errors"
	"fmt"
	"os"
	"time"

	files "github.com/ipfs/boxo/files"
	dag "github.com/ipfs/boxo/ipld/merkledag"
	pb "github.com/ipfs/boxo/ipld/unixfs/pb"
	ipld "github.com/ipfs/go-ipld-format"
	"google.golang.org/protobuf/proto"
)

// A LinkResult for any parallel enumeration of links
// TODO: Should this live in go-ipld-format?
type LinkResult struct {
	Link *ipld.Link
	Err  error
}

// Shorthands for protobuffer types
const (
	TRaw       = pb.Data_Raw
	TFile      = pb.Data_File
	TDirectory = pb.Data_Directory
	TMetadata  = pb.Data_Metadata
	TSymlink   = pb.Data_Symlink
	THAMTShard = pb.Data_HAMTShard
)

// Common errors
var (
	ErrMalformedFileFormat = errors.New("malformed data in file format")
	ErrNotProtoNode        = errors.New("expected a ProtoNode as internal node")
	ErrUnrecognizedType    = errors.New("unrecognized node type")
)

// FromBytes unmarshals a byte slice as protobuf Data.
// Deprecated: Use `FSNodeFromBytes` instead to avoid direct manipulation of `pb.Data`.
func FromBytes(data []byte) (*pb.Data, error) {
	pbdata := new(pb.Data)
	err := proto.Unmarshal(data, pbdata)
	if err != nil {
		return nil, err
	}
	return pbdata, nil
}

// FilePBData creates a protobuf File with the given
// byte slice and returns the marshaled protobuf bytes representing it.
func FilePBData(data []byte, totalsize uint64) []byte {
	pbfile := new(pb.Data)
	typ := pb.Data_File
	pbfile.Type = &typ
	pbfile.Data = data
	pbfile.Filesize = proto.Uint64(totalsize)

	data, err := proto.Marshal(pbfile)
	if err != nil {
		// This really shouldnt happen, i promise
		// The only failure case for marshal is if required fields
		// are not filled out, and they all are. If the proto object
		// gets changed and nobody updates this function, the code
		// should panic due to programmer error
		panic(err)
	}
	return data
}

func FilePBDataWithStat(data []byte, totalsize uint64, mode os.FileMode, mtime time.Time) []byte {
	pbfile := new(pb.Data)
	typ := pb.Data_File
	pbfile.Type = &typ
	pbfile.Data = data
	pbfile.Filesize = proto.Uint64(totalsize)

	pbDataAddStat(pbfile, mode, mtime)

	data, err := proto.Marshal(pbfile)
	if err != nil {
		panic(err)
	}
	return data
}

// FolderPBData returns Bytes that represent a Directory.
func FolderPBData() []byte {
	pbfile := new(pb.Data)
	typ := pb.Data_Directory
	pbfile.Type = &typ

	data, err := proto.Marshal(pbfile)
	if err != nil {
		// this really shouldnt happen, i promise
		panic(err)
	}
	return data
}

func FolderPBDataWithStat(mode os.FileMode, mtime time.Time) []byte {
	pbfile := new(pb.Data)
	typ := pb.Data_Directory
	pbfile.Type = &typ

	pbDataAddStat(pbfile, mode, mtime)

	data, err := proto.Marshal(pbfile)
	if err != nil {
		// this really shouldnt happen, i promise
		panic(err)
	}
	return data
}

func pbDataAddStat(data *pb.Data, mode os.FileMode, mtime time.Time) {
	if mode != 0 {
		data.Mode = proto.Uint32(files.ModePermsToUnixPerms(mode))
	}
	if !mtime.IsZero() {
		data.Mtime = &pb.IPFSTimestamp{
			Seconds: proto.Int64(mtime.Unix()),
		}

		if nanos := uint32(mtime.Nanosecond()); nanos > 0 {
			data.Mtime.Nanos = &nanos
		}
	}
}

// WrapData marshals raw bytes into a `Data_Raw` type protobuf message.
func WrapData(b []byte) []byte {
	pbdata := new(pb.Data)
	typ := pb.Data_Raw
	pbdata.Data = b
	pbdata.Type = &typ
	pbdata.Filesize = proto.Uint64(uint64(len(b)))

	out, err := proto.Marshal(pbdata)
	if err != nil {
		// This shouldnt happen. seriously.
		panic(err)
	}

	return out
}

// SymlinkData returns a `Data_Symlink` protobuf message for the path you specify.
func SymlinkData(path string) ([]byte, error) {
	pbdata := new(pb.Data)
	typ := pb.Data_Symlink
	pbdata.Data = []byte(path)
	pbdata.Type = &typ

	out, err := proto.Marshal(pbdata)
	if err != nil {
		return nil, err
	}

	return out, nil
}

// HAMTShardData return a `Data_HAMTShard` protobuf message
func HAMTShardData(data []byte, fanout uint64, hashType uint64) ([]byte, error) {
	pbdata := new(pb.Data)
	typ := pb.Data_HAMTShard
	pbdata.Type = &typ
	pbdata.HashType = proto.Uint64(hashType)
	pbdata.Data = data
	pbdata.Fanout = proto.Uint64(fanout)

	out, err := proto.Marshal(pbdata)
	if err != nil {
		return nil, err
	}

	return out, nil
}

// UnwrapData unmarshals a protobuf messages and returns the contents.
func UnwrapData(data []byte) ([]byte, error) {
	pbdata := new(pb.Data)
	err := proto.Unmarshal(data, pbdata)
	if err != nil {
		return nil, err
	}
	return pbdata.GetData(), nil
}

// DataSize returns the size of the contents in protobuf wrapped slice.
// For raw data it simply provides the length of it. For Data_Files, it
// will return the associated filesize. Note that Data_Directories will
// return an error.
func DataSize(data []byte) (uint64, error) {
	pbdata := new(pb.Data)
	err := proto.Unmarshal(data, pbdata)
	if err != nil {
		return 0, err
	}
	return size(pbdata)
}

func size(pbdata *pb.Data) (uint64, error) {
	switch pbdata.GetType() {
	case pb.Data_Directory, pb.Data_HAMTShard:
		return 0, errors.New("can't get data size of directory")
	case pb.Data_File, pb.Data_Raw:
		return pbdata.GetFilesize(), nil
	case pb.Data_Symlink:
		return uint64(len(pbdata.GetData())), nil
	default:
		return 0, errors.New("unrecognized node data type")
	}
}

// An FSNode represents a filesystem object using the UnixFS specification.
//
// The `NewFSNode` constructor should be used instead of just calling `new(FSNode)`
// to guarantee that the required (`Type` and `Filesize`) fields in the `format`
// structure are initialized before marshaling (in `GetBytes()`).
type FSNode struct {
	// UnixFS format defined as a protocol buffers message.
	format pb.Data
}

// FSNodeFromBytes unmarshal a protobuf message onto an FSNode.
func FSNodeFromBytes(b []byte) (*FSNode, error) {
	n := new(FSNode)
	err := proto.Unmarshal(b, &n.format)
	if err != nil {
		return nil, err
	}

	return n, nil
}

// NewFSNode creates a new FSNode structure with the given `dataType`.
//
// It initializes the (required) `Type` field (that doesn't have a `Set()`
// accessor so it must be specified at creation), otherwise the `Marshal()`
// method in `GetBytes()` would fail (`required field "Type" not set`).
//
// It also initializes the `Filesize` pointer field to ensure its value
// is never nil before marshaling, this is not a required field but it is
// done to be backwards compatible with previous `go-ipfs` versions hash.
// (If it wasn't initialized there could be cases where `Filesize` could
// have been left at nil, when the `FSNode` was created but no data or
// child nodes were set to adjust it, as is the case in `NewLeaf()`.)
func NewFSNode(dataType pb.Data_DataType) *FSNode {
	n := new(FSNode)
	n.format.Type = &dataType

	// Initialize by `Filesize` by updating it with a dummy (zero) value.
	n.UpdateFilesize(0)

	return n
}

// HashType gets hash type of format
func (n *FSNode) HashType() uint64 {
	return n.format.GetHashType()
}

// Fanout gets fanout of format
func (n *FSNode) Fanout() uint64 {
	return n.format.GetFanout()
}

// AddBlockSize adds the size of the next child block of this node
func (n *FSNode) AddBlockSize(s uint64) {
	n.UpdateFilesize(int64(s))
	n.format.Blocksizes = append(n.format.Blocksizes, s)
}

// RemoveBlockSize removes the given child block's size.
func (n *FSNode) RemoveBlockSize(i int) {
	n.UpdateFilesize(-int64(n.format.Blocksizes[i]))
	n.format.Blocksizes = append(n.format.Blocksizes[:i], n.format.Blocksizes[i+1:]...)
}

// BlockSize returns the block size indexed by `i`.
// TODO: Evaluate if this function should be bounds checking.
func (n *FSNode) BlockSize(i int) uint64 {
	return n.format.Blocksizes[i]
}

// BlockSizes gets blocksizes of format
func (n *FSNode) BlockSizes() []uint64 {
	return n.format.GetBlocksizes()
}

// RemoveAllBlockSizes removes all the child block sizes of this node.
func (n *FSNode) RemoveAllBlockSizes() {
	n.format.Blocksizes = []uint64{}
	n.format.Filesize = proto.Uint64(uint64(len(n.Data())))
}

// GetBytes marshals this node as a protobuf message.
func (n *FSNode) GetBytes() ([]byte, error) {
	return proto.Marshal(&n.format)
}

// FileSize returns the size of the file.
func (n *FSNode) FileSize() uint64 {
	// XXX: This needs to be able to return an error when we don't know the
	// size.
	size, _ := size(&n.format)
	return size
}

// NumChildren returns the number of child blocks of this node
func (n *FSNode) NumChildren() int {
	return len(n.format.Blocksizes)
}

// Data retrieves the `Data` field from the internal `format`.
func (n *FSNode) Data() []byte {
	return n.format.GetData()
}

// SetData sets the `Data` field from the internal `format`
// updating its `Filesize`.
func (n *FSNode) SetData(newData []byte) {
	n.UpdateFilesize(int64(len(newData) - len(n.Data())))
	n.format.Data = newData
}

// UpdateFilesize updates the `Filesize` field from the internal `format`
// by a signed difference (`filesizeDiff`).
// TODO: Add assert to check for `Filesize` > 0?
func (n *FSNode) UpdateFilesize(filesizeDiff int64) {
	n.format.Filesize = proto.Uint64(uint64(
		int64(n.format.GetFilesize()) + filesizeDiff))
}

// Type retrieves the `Type` field from the internal `format`.
func (n *FSNode) Type() pb.Data_DataType {
	return n.format.GetType()
}

// IsDir checks whether the node represents a directory
func (n *FSNode) IsDir() bool {
	switch n.Type() {
	case pb.Data_Directory, pb.Data_HAMTShard:
		return true
	default:
		return false
	}
}

// Mode returns the optionally stored file permissions
func (n *FSNode) Mode() (m os.FileMode) {
	perms := n.format.GetMode() & 0xFFF
	if perms != 0 {
		m = files.UnixPermsToModePerms(perms)
		switch n.Type() {
		case pb.Data_Directory, pb.Data_HAMTShard:
			m |= os.ModeDir
		case pb.Data_Symlink:
			m |= os.ModeSymlink
		}
	}
	return m
}

// SetMode stores the given mode permissions, or nullifies stored permissions
// if none were provided and there are no extended bits set.
func (n *FSNode) SetMode(m os.FileMode) {
	n.SetModeFromUnixPermissions(files.ModePermsToUnixPerms(m))
}

// SetModeFromUnixPermissions stores the given unix permissions, or nullifies stored permissions
// if none were provided and there are no extended bits set.
func (n *FSNode) SetModeFromUnixPermissions(unixPerms uint32) {
	// preserve existing most significant 20 bits
	newMode := (n.format.GetMode() & 0xFFFFF000) | (unixPerms & 0xFFF)

	if unixPerms == 0 {
		if newMode&0xFFFFF000 == 0 {
			n.format.Mode = nil
			return
		}
	}
	n.format.Mode = &newMode
}

// ExtendedMode returns the 20 bits of extended file mode
func (n *FSNode) ExtendedMode() uint32 {
	return (n.format.GetMode() & 0xFFFFF000) >> 12
}

// SetExtendedMode stores the 20 bits of extended file mode, only the first
// 20 bits of the `mode` argument are used, the remaining 12 bits are ignored.
func (n *FSNode) SetExtendedMode(mode uint32) {
	newMode := (mode << 12) | (0xFFF & n.format.GetMode())
	if newMode == 0 {
		n.format.Mode = nil
	} else {
		n.format.Mode = &newMode
	}
}

// ModTime returns the stored last modified timestamp if available.
func (n *FSNode) ModTime() time.Time {
	ts := n.format.GetMtime()
	if ts == nil || ts.Seconds == nil {
		return time.Time{}
	}
	if ts.Nanos == nil {
		return time.Unix(*ts.Seconds, 0)
	}
	if *ts.Nanos < 1 || *ts.Nanos > 999999999 {
		return time.Time{}
	}

	return time.Unix(*ts.Seconds, int64(*ts.Nanos))
}

// SetModTime stores the given last modified timestamp, otherwise nullifies stored timestamp.
func (n *FSNode) SetModTime(ts time.Time) {
	if ts.IsZero() {
		n.format.Mtime = nil
		return
	}

	if n.format.Mtime == nil {
		n.format.Mtime = &pb.IPFSTimestamp{}
	}

	n.format.Mtime.Seconds = proto.Int64(ts.Unix())
	if ts.Nanosecond() > 0 {
		n.format.Mtime.Nanos = proto.Uint32(uint32(ts.Nanosecond()))
	} else {
		n.format.Mtime.Nanos = nil
	}
}

// Metadata is used to store additional FSNode information.
type Metadata struct {
	MimeType string
	Size     uint64
}

// MetadataFromBytes Unmarshals a protobuf Data message into Metadata.
// The provided slice should have been encoded with BytesForMetadata().
func MetadataFromBytes(b []byte) (*Metadata, error) {
	pbd := new(pb.Data)
	err := proto.Unmarshal(b, pbd)
	if err != nil {
		return nil, err
	}
	if pbd.GetType() != pb.Data_Metadata {
		return nil, errors.New("incorrect node type")
	}

	pbm := new(pb.Metadata)
	err = proto.Unmarshal(pbd.Data, pbm)
	if err != nil {
		return nil, err
	}
	md := new(Metadata)
	md.MimeType = pbm.GetMimeType()
	return md, nil
}

// Bytes marshals Metadata as a protobuf message of Metadata type.
func (m *Metadata) Bytes() ([]byte, error) {
	pbm := new(pb.Metadata)
	pbm.MimeType = &m.MimeType
	return proto.Marshal(pbm)
}

// BytesForMetadata wraps the given Metadata as a profobuf message of Data type,
// setting the DataType to Metadata. The wrapped bytes are itself the
// result of calling m.Bytes().
func BytesForMetadata(m *Metadata) ([]byte, error) {
	pbd := new(pb.Data)
	pbd.Filesize = proto.Uint64(m.Size)
	typ := pb.Data_Metadata
	pbd.Type = &typ
	mdd, err := m.Bytes()
	if err != nil {
		return nil, err
	}

	pbd.Data = mdd
	return proto.Marshal(pbd)
}

// EmptyDirNode creates an empty folder Protonode.
func EmptyDirNode() *dag.ProtoNode {
	return dag.NodeWithData(FolderPBData())
}

func EmptyDirNodeWithStat(mode os.FileMode, mtime time.Time) *dag.ProtoNode {
	return dag.NodeWithData(FolderPBDataWithStat(mode, mtime))
}

// EmptyFileNode creates an empty file Protonode.
func EmptyFileNode() *dag.ProtoNode {
	return dag.NodeWithData(FilePBData(nil, 0))
}

// ReadUnixFSNodeData extracts the UnixFS data from an IPLD node.
// Raw nodes are (also) processed because they are used as leaf
// nodes containing (only) UnixFS data.
func ReadUnixFSNodeData(node ipld.Node) (data []byte, err error) {
	switch node := node.(type) {

	case *dag.ProtoNode:
		fsNode, err := FSNodeFromBytes(node.Data())
		if err != nil {
			return nil, fmt.Errorf("incorrectly formatted protobuf: %s", err)
		}

		switch fsNode.Type() {
		case pb.Data_File, pb.Data_Raw:
			return fsNode.Data(), nil
			// Only leaf nodes (of type `Data_Raw`) contain data but due to a
			// bug the `Data_File` type (normally used for internal nodes) is
			// also used for leaf nodes, so both types are accepted here
			// (see the `balanced` package for more details).
		default:
			return nil, fmt.Errorf("found %s node in unexpected place",
				fsNode.Type().String())
		}

	case *dag.RawNode:
		return node.RawData(), nil

	default:
		return nil, ErrUnrecognizedType
		// TODO: To avoid rewriting the error message, but a different error from
		// `unixfs.ErrUnrecognizedType` should be used (defining it in the
		// `merkledag` or `go-ipld-format` packages).
	}
}

// Extract the `unixfs.FSNode` from the `ipld.Node` (assuming this
// was implemented by a `mdag.ProtoNode`).
func ExtractFSNode(node ipld.Node) (*FSNode, error) {
	protoNode, ok := node.(*dag.ProtoNode)
	if !ok {
		return nil, ErrNotProtoNode
	}

	fsNode, err := FSNodeFromBytes(protoNode.Data())
	if err != nil {
		return nil, err
	}

	return fsNode, nil
}
