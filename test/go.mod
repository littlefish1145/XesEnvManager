module test

go 1.19

require (
	google.golang.org/grpc v1.57.0
	google.golang.org/protobuf v1.31.0
)

replace (
	google.golang.org/grpc => ../
	google.golang.org/protobuf => ../
)