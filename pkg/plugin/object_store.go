/*
Copyright 2017 the Heptio Ark contributors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package plugin

import (
	"io"
	"time"

	"github.com/hashicorp/go-plugin"
	"golang.org/x/net/context"
	"google.golang.org/grpc"

	"github.com/heptio/ark/pkg/cloudprovider"
	proto "github.com/heptio/ark/pkg/plugin/generated"
)

const byteChunkSize = 16384

// ObjectStorePlugin is an implementation of go-plugin's Plugin
// interface with support for gRPC for the cloudprovider/ObjectStore
// interface.
type ObjectStorePlugin struct {
	plugin.NetRPCUnsupportedPlugin

	impl cloudprovider.ObjectStore
}

// NewObjectStorePlugin construct an ObjectStorePlugin.
func NewObjectStorePlugin(objectStore cloudprovider.ObjectStore) *ObjectStorePlugin {
	return &ObjectStorePlugin{
		impl: objectStore,
	}
}

// GRPCServer registers an ObjectStore gRPC server.
func (p *ObjectStorePlugin) GRPCServer(s *grpc.Server) error {
	proto.RegisterObjectStoreServer(s, &ObjectStoreGRPCServer{impl: p.impl})
	return nil
}

// GRPCClient returns an ObjectStore gRPC client.
func (p *ObjectStorePlugin) GRPCClient(c *grpc.ClientConn) (interface{}, error) {
	return &ObjectStoreGRPCClient{grpcClient: proto.NewObjectStoreClient(c)}, nil
}

// ObjectStoreGRPCClient implements the cloudprovider.ObjectStore interface and uses a
// gRPC client to make calls to the plugin server.
type ObjectStoreGRPCClient struct {
	grpcClient proto.ObjectStoreClient
}

// Init prepares the ObjectStore for usage using the provided map of
// configuration key-value pairs. It returns an error if the ObjectStore
// cannot be initialized from the provided config.
func (c *ObjectStoreGRPCClient) Init(config map[string]string) error {
	_, err := c.grpcClient.Init(context.Background(), &proto.InitRequest{Config: config})

	return err
}

// PutObject creates a new object using the data in body within the specified
// object storage bucket with the given key.
func (c *ObjectStoreGRPCClient) PutObject(bucket, key string, body io.Reader) error {
	stream, err := c.grpcClient.PutObject(context.Background())
	if err != nil {
		return err
	}

	// read from the provider io.Reader into chunks, and send each one over
	// the gRPC stream
	chunk := make([]byte, byteChunkSize)
	for {
		n, err := body.Read(chunk)
		if err == io.EOF {
			_, resErr := stream.CloseAndRecv()
			return resErr
		}
		if err != nil {
			stream.CloseSend()
			return err
		}

		if err := stream.Send(&proto.PutObjectRequest{Bucket: bucket, Key: key, Body: chunk[0:n]}); err != nil {
			return err
		}
	}
}

// GetObject retrieves the object with the given key from the specified
// bucket in object storage.
func (c *ObjectStoreGRPCClient) GetObject(bucket, key string) (io.ReadCloser, error) {
	stream, err := c.grpcClient.GetObject(context.Background(), &proto.GetObjectRequest{Bucket: bucket, Key: key})
	if err != nil {
		return nil, err
	}

	receive := func() ([]byte, error) {
		data, err := stream.Recv()
		if err != nil {
			return nil, err
		}

		return data.Data, nil
	}

	close := func() error {
		return stream.CloseSend()
	}

	return &StreamReadCloser{receive: receive, close: close}, nil
}

// ListCommonPrefixes gets a list of all object key prefixes that come
// before the provided delimiter (this is often used to simulate a directory
// hierarchy in object storage).
func (c *ObjectStoreGRPCClient) ListCommonPrefixes(bucket, delimiter string) ([]string, error) {
	res, err := c.grpcClient.ListCommonPrefixes(context.Background(), &proto.ListCommonPrefixesRequest{Bucket: bucket, Delimiter: delimiter})
	if err != nil {
		return nil, err
	}

	return res.Prefixes, nil
}

// ListObjects gets a list of all objects in bucket that have the same prefix.
func (c *ObjectStoreGRPCClient) ListObjects(bucket, prefix string) ([]string, error) {
	res, err := c.grpcClient.ListObjects(context.Background(), &proto.ListObjectsRequest{Bucket: bucket, Prefix: prefix})
	if err != nil {
		return nil, err
	}

	return res.Keys, nil
}

// DeleteObject removes object with the specified key from the given
// bucket.
func (c *ObjectStoreGRPCClient) DeleteObject(bucket, key string) error {
	_, err := c.grpcClient.DeleteObject(context.Background(), &proto.DeleteObjectRequest{Bucket: bucket, Key: key})

	return err
}

// CreateSignedURL creates a pre-signed URL for the given bucket and key that expires after ttl.
func (c *ObjectStoreGRPCClient) CreateSignedURL(bucket, key string, ttl time.Duration) (string, error) {
	res, err := c.grpcClient.CreateSignedURL(context.Background(), &proto.CreateSignedURLRequest{
		Bucket: bucket,
		Key:    key,
		Ttl:    int64(ttl),
	})
	if err != nil {
		return "", nil
	}

	return res.Url, nil
}

// ObjectStoreGRPCServer implements the proto-generated ObjectStoreServer interface, and accepts
// gRPC calls and forwards them to an implementation of the pluggable interface.
type ObjectStoreGRPCServer struct {
	impl cloudprovider.ObjectStore
}

// Init prepares the ObjectStore for usage using the provided map of
// configuration key-value pairs. It returns an error if the ObjectStore
// cannot be initialized from the provided config.
func (s *ObjectStoreGRPCServer) Init(ctx context.Context, req *proto.InitRequest) (*proto.Empty, error) {
	if err := s.impl.Init(req.Config); err != nil {
		return nil, err
	}

	return &proto.Empty{}, nil
}

// PutObject creates a new object using the data in body within the specified
// object storage bucket with the given key.
func (s *ObjectStoreGRPCServer) PutObject(stream proto.ObjectStore_PutObjectServer) error {
	// we need to read the first chunk ahead of time to get the bucket and key;
	// in our receive method, we'll use `first` on the first call
	firstChunk, err := stream.Recv()
	if err != nil {
		return err
	}

	bucket := firstChunk.Bucket
	key := firstChunk.Key

	receive := func() ([]byte, error) {
		if firstChunk != nil {
			res := firstChunk.Body
			firstChunk = nil
			return res, nil
		}

		data, err := stream.Recv()
		if err != nil {
			return nil, err
		}
		return data.Body, nil
	}

	close := func() error {
		return nil
	}

	if err := s.impl.PutObject(bucket, key, &StreamReadCloser{receive: receive, close: close}); err != nil {
		return err
	}

	return stream.SendAndClose(&proto.Empty{})
}

// GetObject retrieves the object with the given key from the specified
// bucket in object storage.
func (s *ObjectStoreGRPCServer) GetObject(req *proto.GetObjectRequest, stream proto.ObjectStore_GetObjectServer) error {
	rdr, err := s.impl.GetObject(req.Bucket, req.Key)
	if err != nil {
		return err
	}

	chunk := make([]byte, byteChunkSize)
	for {
		n, err := rdr.Read(chunk)
		if err != nil && err != io.EOF {
			return err
		}
		if n == 0 {
			return nil
		}

		if err := stream.Send(&proto.Bytes{Data: chunk[0:n]}); err != nil {
			return err
		}
	}
}

// ListCommonPrefixes gets a list of all object key prefixes that come
// before the provided delimiter (this is often used to simulate a directory
// hierarchy in object storage).
func (s *ObjectStoreGRPCServer) ListCommonPrefixes(ctx context.Context, req *proto.ListCommonPrefixesRequest) (*proto.ListCommonPrefixesResponse, error) {
	prefixes, err := s.impl.ListCommonPrefixes(req.Bucket, req.Delimiter)
	if err != nil {
		return nil, err
	}

	return &proto.ListCommonPrefixesResponse{Prefixes: prefixes}, nil
}

// ListObjects gets a list of all objects in bucket that have the same prefix.
func (s *ObjectStoreGRPCServer) ListObjects(ctx context.Context, req *proto.ListObjectsRequest) (*proto.ListObjectsResponse, error) {
	keys, err := s.impl.ListObjects(req.Bucket, req.Prefix)
	if err != nil {
		return nil, err
	}

	return &proto.ListObjectsResponse{Keys: keys}, nil
}

// DeleteObject removes object with the specified key from the given
// bucket.
func (s *ObjectStoreGRPCServer) DeleteObject(ctx context.Context, req *proto.DeleteObjectRequest) (*proto.Empty, error) {
	err := s.impl.DeleteObject(req.Bucket, req.Key)
	if err != nil {
		return nil, err
	}

	return &proto.Empty{}, nil
}

// CreateSignedURL creates a pre-signed URL for the given bucket and key that expires after ttl.
func (s *ObjectStoreGRPCServer) CreateSignedURL(ctx context.Context, req *proto.CreateSignedURLRequest) (*proto.CreateSignedURLResponse, error) {
	url, err := s.impl.CreateSignedURL(req.Bucket, req.Key, time.Duration(req.Ttl))
	if err != nil {
		return nil, err
	}

	return &proto.CreateSignedURLResponse{Url: url}, nil
}
