/* tinyparse: reads a file whose first byte is a name length, then the name. */
#include <stdio.h>
#include <stdlib.h>
#include <string.h>

struct header {
	char *name;
	size_t len;
};

__attribute__((noinline)) static int parse_header(const unsigned char *data, size_t size, struct header *h)
{
	if (size < 1)
		return -1;
	size_t n = data[0];
	if (n > size - 1)
		return -1;
	if (n > 16)
		return -1;
	h->name = malloc(16);
	if (h->name == NULL)
		return -1;
	memcpy(h->name, data + 1, n);
	h->len = n;
	return 0;
}

__attribute__((noinline)) static int parse_file(const char *path)
{
	unsigned char buf[512];
	FILE *f = fopen(path, "rb");
	if (f == NULL)
		return -1;
	size_t size = fread(buf, 1, sizeof(buf), f);
	fclose(f);
	struct header h = {0};
	int rc = parse_header(buf, size, &h);
	free(h.name);
	return rc;
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
	printf("ok\n");
	return 0;
}
