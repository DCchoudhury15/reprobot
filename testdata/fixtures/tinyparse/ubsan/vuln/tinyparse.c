/* tinyparse (table version): reads a record count, 3 bytes big endian, and
 * says how much memory the table would need. */
#include <stdio.h>

#define RECORD_SIZE 256

__attribute__((noinline)) static int table_bytes(const unsigned char *p)
{
	int count = (p[0] << 16) | (p[1] << 8) | p[2];
	return count * RECORD_SIZE;
}

__attribute__((noinline)) static int parse_file(const char *path)
{
	unsigned char buf[3];
	FILE *f = fopen(path, "rb");
	if (f == NULL)
		return -1;
	size_t size = fread(buf, 1, sizeof(buf), f);
	fclose(f);
	if (size != sizeof(buf))
		return -1;
	int bytes = table_bytes(buf);
	if (bytes < 0)
		return -1;
	printf("table needs %d bytes\n", bytes);
	return 0;
}

int main(int argc, char **argv)
{
	if (argc != 2) {
		fprintf(stderr, "usage: tinyparse FILE\n");
		return 2;
	}
	if (parse_file(argv[1]) != 0) {
		fprintf(stderr, "tinyparse: bad input\n");
		return 1;
	}
	return 0;
}
