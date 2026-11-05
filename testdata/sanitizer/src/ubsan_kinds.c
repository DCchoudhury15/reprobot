/* One UBSan error per mode, picked by argv[1]. */
#include <stdio.h>
#include <string.h>

struct point {
	int x;
	int y;
};

int shift_left(int v, int by)
{
	return v << by;
}

int load_int(const int *p)
{
	return *p;
}

int point_x(const struct point *p)
{
	return p->x;
}

int pick(int i)
{
	int table[4] = {1, 2, 3, 4};
	return table[i];
}

int read_misaligned(const char *buf)
{
	return *(const int *)(buf + 1);
}

int divide(int a, int b)
{
	return a / b;
}

int main(int argc, char **argv)
{
	const char *mode = argc > 1 ? argv[1] : "";
	int k = argc;
	char buf[16] = {0};
	if (strcmp(mode, "shift") == 0)
		printf("%d\n", shift_left(1, 30 + k));
	else if (strcmp(mode, "null") == 0)
		printf("%d\n", load_int(k > 100 ? &k : NULL));
	else if (strcmp(mode, "member") == 0)
		printf("%d\n", point_x(k > 100 ? (const struct point *)buf : NULL));
	else if (strcmp(mode, "index") == 0)
		printf("%d\n", pick(k + 2));
	else if (strcmp(mode, "misaligned") == 0)
		printf("%d\n", read_misaligned(buf));
	else if (strcmp(mode, "div") == 0)
		printf("%d\n", divide(k, k - 2));
	return 0;
}
